// Package radio is the 24/7 loop with a PERSISTENT source + prefetch:
//   - ONE ffmpeg master (package icecast) stays connected to Icecast forever.
//   - A producer goroutine builds the NEXT tanda (GLM+qohl voices) while the
//     current one plays, so the master always has PCM to encode → it never
//     starves → icecast never drops the source → no 404 between tandas.
//
// Now-playing is set synchronously when each track starts (no timing drift).
package radio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"radio-dj/internal/config"
	"radio-dj/internal/dj"
	"radio-dj/internal/i18n"
	"radio-dj/internal/icecast"
	"radio-dj/internal/library"
	"radio-dj/internal/musicfeed"
	"radio-dj/internal/news"
	"radio-dj/internal/skills"
	"radio-dj/internal/status"
	"radio-dj/internal/supervisor"
	"radio-dj/internal/voice"
)

// Segment is one item fed to the streamer: a DJ voice clip or a music track.
type Segment struct {
	Path         string
	IsVoice      bool
	IsNews       bool
	IsDJ         bool
	News         *news.Item
	NewsItems    []news.Item
	Program      *news.ProgramSlot // non-nil for the scheduled slot this bulletin fulfils
	LiveTime     bool              // voice generated at air-time (clock skill) — no pre-baked Path
	Midroll      bool              // voice fires mid-song (~50%), not at the start
	Meta         library.Track     // valid when !IsVoice
	Text         string            // DJ speech text — logged at air-time, not build-time
	Req          string            // request text that matched this track — air-time log
	NewsFallback bool              // music filler that may be replaced by a newly READY news break
}

type preparedTanda struct {
	mode       string
	generation uint64
	segs       []Segment
}

// djLogPath is set in Serve(); logDJ appends DJ speech, requests and the
// spoken clock to it so /dj-log can surface what aired (the feedback view).
var djLogPath string

type playbackMode struct {
	mu         sync.RWMutex
	mode       string
	generation uint64
}

func newPlaybackMode(mode string) *playbackMode { return &playbackMode{mode: mode} }
func (p *playbackMode) Get() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.mode
}
func (p *playbackMode) Snapshot() (string, uint64) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.mode, p.generation
}
func (p *playbackMode) Set(mode string) {
	p.mu.Lock()
	p.mode = mode
	p.generation++
	p.mu.Unlock()
}

func logDJ(kind, text string) {
	if djLogPath == "" {
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	// One JSON object per line (JSONL) — the /dj-log reader parses structured
	// entries instead of regex-matching a human format, so kind/text with
	// arbitrary characters can't break the parse.
	b, _ := json.Marshal(status.DJLogEntry{T: time.Now().Format("15:04:05"), Kind: kind, Text: text})
	if f, err := os.OpenFile(djLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		_, _ = f.Write(append(b, '\n'))
		_ = f.Close()
	}
}

// Serve runs the station until fatally errored. The streamer is opened once
// and kept alive across the whole loop.
func Serve(cfg config.Config) error {
	lib, err := library.New(cfg.Source, cfg.Library, cfg.NavidromeURL, cfg.NavidromeUser, cfg.NavidromePass)
	if err != nil {
		return fmt.Errorf("library: %w", err)
	}
	st := status.New(cfg.StateDir, cfg.NeedsSetup(), cfg.IcecastMount)
	st.SetLanguage(cfg.Language)
	newsSources := make([]string, 0, len(cfg.NewsFeeds))
	seenNewsSources := map[string]bool{}
	for _, feed := range cfg.NewsFeeds {
		if feed.Name != "" && !seenNewsSources[feed.Name] {
			seenNewsSources[feed.Name] = true
			newsSources = append(newsSources, feed.Name)
		}
	}
	st.SetNewsSources(newsSources)
	modes := newPlaybackMode(cfg.PlayMode)
	newsQueue := news.NewQueue(cfg.StateDir)
	newsStore := news.NewStore()
	collectorCtx, stopCollectors := context.WithCancel(context.Background())
	defer stopCollectors()
	djLogPath = filepath.Join(cfg.StateDir, "dj-log.txt") // /dj-log tails this for feedback
	st.ListenAndServeHTTP(cfg.StatusPort)
	log.Printf("[radio-dj] UI :%d · stream :%d%s · POST /request", cfg.StatusPort, cfg.IcecastPort, cfg.IcecastMount)

	var djx *dj.DJ
	var vox *voice.Voice
	var pool *skills.Pool
	if cfg.DJEnabled {
		prompts, perr := i18n.Load(cfg.Language)
		if perr != nil {
			log.Printf("[radio-dj] WARN i18n: %v", perr)
		}
		djx = dj.New(cfg.LLMProvider, cfg.GLMBaseURL, cfg.GLMAPIKey, cfg.GLMModel, cfg.StationName, cfg.LocationName, prompts)
		vox = voice.New(cfg.VoiceProvider, cfg.Voice, cfg.VoiceCmd)
		pool = skills.NewPool(cfg.StationName, cfg.LocationName, cfg.Latitude, cfg.Longitude, skills.LoadDir(cfg.StateDir, cfg.Language))
		log.Printf("[radio-dj] DJ on: %s @ %s · every %d · bed=%s",
			cfg.GLMModel, cfg.LocationName, cfg.DJEvery, or(cfg.Bed, "(none)"))
	} else {
		log.Printf("[radio-dj] DJ off (ZAI_API_KEY + RDJ_VOICE_CMD to enable)")
	}

	// News rendering is its own background pipeline. It fills a tiny queue of
	// complete audio breaks while music is already available, so mode switches
	// and track starts never wait on RSS, OGP, Ollama, edge-tts, or ffmpeg.
	news.StartCollectors(collectorCtx, newsStore, toNewsFeeds(cfg.NewsFeeds))
	if cfg.Source == "folder" && cfg.AutoMusicEnabled {
		musicfeed.Start(collectorCtx, cfg.AutoMusicTempDir, cfg.StateDir)
		log.Printf("[musicfeed] automatic open-music pool enabled: %s", cfg.AutoMusicTempDir)
	}
	programClock := news.NewProgramClock()
	newsPrep := startNewsPreloader(cfg, djx, vox, newsQueue, newsStore, programClock, st)
	st.SetNewsPriorityHandler(newsPrep.SetPriority)
	st.SetNewsSourceHandler(newsPrep.SetSource)

	// Bring up icecast ourselves unless an external one is configured.
	srcPw := cfg.IcecastSourcePW
	if srcPw == "" {
		ic, ierr := supervisor.EnsureIcecast(cfg.StateDir, cfg.IcecastHost, cfg.IcecastPort, cfg.IcecastMount)
		if ierr != nil {
			return fmt.Errorf("ensure icecast: %w", ierr)
		}
		srcPw = ic.SourcePassword()
		st.SetIcecast(fmt.Sprintf("http://%s:%d", cfg.IcecastHost, cfg.IcecastPort), ic.AdminPassword())
		log.Printf("[radio-dj] icecast supervisado (source pw %s…)", srcPw[:8])
	}

	streamer, err := icecast.OpenStreamer(cfg.IcecastHost, cfg.IcecastPort, cfg.IcecastMount, cfg.Encoder, srcPw, cfg.StationName, cfg.Bitrate)
	if err != nil {
		return fmt.Errorf("open streamer: %w", err)
	}
	defer func() { streamer.Close() }() // closure: close whatever streamer is current at exit
	// controls: skip/previous from the player UI. Buffered(1) so rapid clicks
	// coalesce into one pending action; the loop drains it after Play returns.
	controls := make(chan string, 1)
	var controlMu sync.Mutex
	var activePlaybackGeneration atomic.Uint64 // encoded as generation+1; zero means between segments
	playForGeneration := func(path string, generation uint64) error {
		encoded := generation + 1
		activePlaybackGeneration.Store(encoded)
		defer activePlaybackGeneration.CompareAndSwap(encoded, 0)
		return streamer.Play(path)
	}
	takeControl := func() string {
		controlMu.Lock()
		defer controlMu.Unlock()
		select {
		case action := <-controls:
			return action
		default:
			return ""
		}
	}
	st.SetControlHandler(func(action string) bool {
		controlMu.Lock()
		defer controlMu.Unlock()
		// SkipCurrent kills the in-flight decoder immediately; reject if no
		// decoder is active (between songs) or a skip is already pending.
		if len(controls) > 0 || !streamer.SkipCurrent() {
			return false
		}
		controls <- action
		return true
	})
	st.SetModeHandler(cfg.PlayMode, func(mode string) bool {
		if err := config.SavePlaybackMode(cfg.StateDir, mode); err != nil {
			log.Printf("[mode] save: %v", err)
			return false
		}
		_, oldGeneration := modes.Snapshot()
		modes.Set(mode)
		if mode == "news" {
			newsPrep.Wake()
		}
		// Cut the current decoder so the consumer drops the old tanda and
		// begins the newly selected mode without waiting for a whole song.
		controlMu.Lock()
		stopped := streamer.SkipCurrent()
		controlMu.Unlock()
		if !stopped {
			// SetCurrent and ffmpeg's decoder registration are not atomic. If the
			// click lands in that gap, retry only while an old-generation segment
			// is active; never kill audio that already belongs to the new mode.
			go func(staleGeneration uint64) {
				deadline := time.Now().Add(2 * time.Second)
				encodedStale := staleGeneration + 1
				for time.Now().Before(deadline) {
					active := activePlaybackGeneration.Load()
					if active > encodedStale {
						return
					}
					if active == encodedStale {
						controlMu.Lock()
						stopped := streamer.SkipCurrent()
						controlMu.Unlock()
						if stopped {
							return
						}
					}
					time.Sleep(20 * time.Millisecond)
				}
			}(oldGeneration)
		}
		return true
	})
	st.MarkPlaying(true)
	log.Printf("[radio-dj] source persistente ON AIR ✓")

	// Health watch: a sleeping laptop or network change can leave the master
	// ffmpeg alive but with a dead (half-open) Icecast output — Alive() won't
	// catch it, so the mount silently 404s. Poll Icecast's status-json every
	// 30s; if our mount has no source for ~60s while the master claims running,
	// the output is dead — kill the stuck process so the reopen path recovers.
	go func() {
		statusURL := fmt.Sprintf("http://%s:%d/status-json.xsl", cfg.IcecastHost, cfg.IcecastPort)
		needle := []byte(cfg.IcecastMount)
		misses := 0
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for range t.C {
			has := false
			if resp, err := http.Get(statusURL); err == nil {
				body, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				has = bytes.Contains(body, needle)
			}
			if has {
				misses = 0
				continue
			}
			misses++
			if misses < 2 {
				continue
			}
			controlMu.Lock()
			if streamer.Alive() {
				log.Printf("[radio-dj] icecast sin source %s — master zombie, forzando reopen", cfg.IcecastMount)
				streamer.KillMaster()
			}
			controlMu.Unlock()
			misses = 0
		}
	}()

	// Producer: prefetch tandas so the master never starves.
	prepared := make(chan preparedTanda, 2)
	var queuedNewsBatches atomic.Int32
	var activeNewsFallbacks atomic.Int32
	go func() {
		tc := 0
		for {
			mode, generation := modes.Snapshot()
			// One filler is enough in News Continuous. While it is on air, leave
			// newly rendered bulletins in the READY queue where the watcher can see
			// them, instead of filling the prefetch channel with more music.
			if (mode == "news" || mode == "radio") && activeNewsFallbacks.Load() > 0 {
				time.Sleep(250 * time.Millisecond)
				continue
			}
			segs, reqs, berr := buildTanda(cfg, lib, djx, vox, pool, st, mode, newsPrep, programClock, &tc)
			if berr != nil {
				log.Printf("[radio-dj] build: %v — retry 10s", berr)
				time.Sleep(10 * time.Second)
				continue
			}
			log.Printf("[radio-dj] tanda lista (%d segmentos%s) — prefetched", len(segs), reqs)
			if segmentsContainNews(segs) {
				queuedNewsBatches.Add(1)
			}
			if segmentsContainNewsFallback(segs) {
				activeNewsFallbacks.Add(1)
			}
			prepared <- preparedTanda{mode: mode, generation: generation, segs: segs}
		}
	}()

	// Consumer: play each tanda as it arrives; the next is already being built.
	var previousTrack *Segment
	tandaN := 0
	for batch := range prepared {
		batchHasNews := segmentsContainNews(batch.segs)
		batchHasFallback := segmentsContainNewsFallback(batch.segs)
		if batchHasNews {
			queuedNewsBatches.Add(-1)
		}
		currentMode, currentGeneration := modes.Snapshot()
		if batch.mode != currentMode || batch.generation != currentGeneration {
			newsPrep.releaseSegments(batch.segs)
			if batchHasFallback {
				activeNewsFallbacks.Add(-1)
			}
			continue // stale prefetch from before a mode switch
		}
		segs := batch.segs
		tandaN++
		log.Printf("[radio-dj] ▶ tanda #%d al aire (%d segmentos)", tandaN, len(segs))
		pendingVoicePath := ""
		pendingVoiceText := ""
		pendingMidrollPath := ""
		pendingMidrollText := ""
		pendingLiveTime := false
		for i := 0; i < len(segs); i++ {
			seg := segs[i]
			currentMode, currentGeneration = modes.Snapshot()
			if batch.mode != currentMode || batch.generation != currentGeneration {
				newsPrep.releaseSegments(segs[i:])
				break // mode changed while this tanda was on air
			}
			// News-only mode uses music while the background renderer catches up.
			// Re-check at the last possible moment: if a bulletin became READY after
			// this batch was prefetched, replace the filler instead of playing a full
			// extra song and making the mode appear unresponsive.
			if seg.NewsFallback {
				var takeSlot *news.ProgramSlot
				shouldTake := currentMode == "news"
				if currentMode == "radio" {
					if slot, due := programClock.Due(time.Now()); due {
						takeSlot = &slot
						shouldTake = true
					}
				}
				if shouldTake {
					if ready, ok := newsPrep.tryTake(takeSlot); ok {
						replacement := make([]Segment, 0, len(segs)-1+len(ready))
						replacement = append(replacement, segs[:i]...)
						replacement = append(replacement, ready...)
						replacement = append(replacement, segs[i+1:]...)
						segs = replacement
						i--
						continue
					}
				}
			}
			if seg.IsNews {
				newsPrep.markAired(seg)
				if seg.Program != nil {
					programClock.MarkAired(*seg.Program)
				}
				st.SetCurrent(toNewsStatus(*seg.News, seg.Text), toStatus(nextTrack(segs, i)))
				logDJ(status.LogKindDJ, seg.Text)
				if err := playForGeneration(seg.Path, batch.generation); err != nil {
					log.Printf("[news] segment error: %v", err)
				}
				newsPrep.cleanupSegment(seg)
				continue
			}
			if seg.IsDJ {
				if seg.News != nil {
					// A post-news discussion belongs to the article that prompted it.
					// Keep its title, source and image visible for the whole talk.
					st.SetCurrent(toNewsStatus(*seg.News, seg.Text), toStatus(nextTrack(segs, i)))
				} else {
					st.SetCurrent(status.Track{Type: "dj", Title: "AI DJ", SpeechText: seg.Text}, toStatus(nextTrack(segs, i)))
				}
				logDJ(status.LogKindDJ, seg.Text)
				if err := playForGeneration(seg.Path, batch.generation); err != nil {
					log.Printf("[dj] segment error: %v", err)
				}
				newsPrep.cleanupSegment(seg)
				continue
			}
			if seg.IsVoice {
				switch {
				case seg.LiveTime:
					pendingLiveTime = true // clock skill — voice built at air-time
				case seg.Midroll:
					pendingMidrollPath = seg.Path // fire mid-song (~50%)
					pendingMidrollText = seg.Text
				default:
					pendingVoicePath = seg.Path // overlay over the next song (live ducking)
					pendingVoiceText = seg.Text
				}
				continue
			}
		playCurrent:
			st.SetCurrent(toStatus(seg.Meta), toStatus(nextTrack(segs, i)))
			log.Printf("▶ %s — %s", seg.Meta.Title, seg.Meta.Artist)
			if seg.Req != "" {
				logDJ(status.LogKindReq, seg.Req) // air-time: the requested track starts now
			}
			if pendingLiveTime {
				// clock skill: generate the voice NOW so the hour isn't stale.
				// Song is already playing (ducked via Interject), so GLM+TTS
				// latency (2-5s) hides under it — no dead air.
				pendingLiveTime = false
				go func() {
					text := djx.Say(pool.Prompt("time", map[string]string{"time": time.Now().Format("15:04")}))
					logDJ(status.LogKindTime, text)
					vf, verr := vox.Speak(text)
					if verr != nil {
						log.Printf("[dj] time voice: %v", verr)
						return
					}
					if err := streamer.Interject(vf); err != nil {
						log.Printf("[dj] interject: %v", err)
					}
				}()
			} else if pendingVoicePath != "" {
				vf := pendingVoicePath
				vt := pendingVoiceText
				pendingVoicePath = ""
				pendingVoiceText = ""
				go func() {
					time.Sleep(700 * time.Millisecond) // let the intro land
					logDJ(status.LogKindDJ, vt)        // air-time: the intro overlays the song now
					if err := streamer.Interject(vf); err != nil {
						log.Printf("[dj] interject: %v", err)
					}
				}()
			}
			// midroll: fire at ~50% of the song duration
			if pendingMidrollPath != "" {
				mf := pendingMidrollPath
				mt := pendingMidrollText
				src := seg.Meta.Src
				pendingMidrollPath = ""
				pendingMidrollText = ""
				go func() {
					dur := library.Duration(src).Seconds()
					if dur < 30 {
						return // too short for midroll
					}
					time.Sleep(time.Duration(dur * 0.5 * float64(time.Second)))
					logDJ(status.LogKindDJ, mt)
					if err := streamer.Interject(mf); err != nil {
						log.Printf("[dj] midroll interject: %v", err)
					}
				}()
			}
			// If the filler started just before a bulletin became ready, end it at
			// once instead of making News Continuous wait for the whole track.
			var newsReadyWatch chan struct{}
			if seg.NewsFallback {
				newsReadyWatch = make(chan struct{})
				go func(done <-chan struct{}) {
					ticker := time.NewTicker(100 * time.Millisecond)
					defer ticker.Stop()
					for {
						select {
						case <-done:
							return
						case <-ticker.C:
							mode := modes.Get()
							newsCanInterrupt := mayInterruptForNews(mode, programClock, time.Now())
							if newsCanInterrupt && (st.NewsReady() || queuedNewsBatches.Load() > 0) {
								controlMu.Lock()
								_ = streamer.SkipCurrent()
								controlMu.Unlock()
								return
							}
						}
					}
				}(newsReadyWatch)
			}
			perr := playForGeneration(seg.Path, batch.generation)
			if newsReadyWatch != nil {
				close(newsReadyWatch)
			}
			control := takeControl()
			if perr != nil && control == "" {
				log.Printf("[radio-dj] segment error: %v", perr)
			}
			cleanupTransient(cfg.AutoMusicTempDir, seg.Path)
			if control == "previous" && previousTrack != nil {
				prev := *previousTrack
				log.Printf("[radio-dj] ◀ replay %s — %s", prev.Meta.Title, prev.Meta.Artist)
				st.SetCurrent(toStatus(prev.Meta), toStatus(seg.Meta))
				_ = playForGeneration(prev.Path, batch.generation)
				// a "next" while replaying the previous track returns to the
				// interrupted current track; discard that consumed command.
				_ = takeControl()
				goto playCurrent
			}
			played := seg
			previousTrack = &played
			if !streamer.Alive() {
				log.Printf("[radio-dj] master caído — reabriendo source")
				// Serialize the swap against the /control handler (it reads
				// `streamer` under controlMu). Retry until OpenStreamer succeeds —
				// a single failure must not leave streamer nil (next Alive() would
				// panic). Keep the closed streamer until a fresh one is ready, so a
				// concurrent /control hits a real (if dead) object, not nil.
				controlMu.Lock()
				streamer.Close()
				controlMu.Unlock()
				for {
					controlMu.Lock()
					ns, rerr := icecast.OpenStreamer(cfg.IcecastHost, cfg.IcecastPort, cfg.IcecastMount, cfg.Encoder, srcPw, cfg.StationName, cfg.Bitrate)
					if rerr == nil {
						streamer = ns
						controlMu.Unlock()
						break
					}
					controlMu.Unlock()
					log.Printf("[radio-dj] reopen failed: %v — retry 5s", rerr)
					time.Sleep(5 * time.Second)
				}
			}
		}
		// tail voice (e.g. outro with no song after it) — overlay over silence
		if pendingLiveTime {
			go func() {
				text := djx.Say(pool.Prompt("time", map[string]string{"time": time.Now().Format("15:04")}))
				logDJ(status.LogKindTime, text)
				if vf, verr := vox.Speak(text); verr == nil {
					_ = streamer.Interject(vf)
				}
			}()
		} else if pendingVoicePath != "" {
			logDJ(status.LogKindDJ, pendingVoiceText)
			if err := streamer.Interject(pendingVoicePath); err != nil {
				log.Printf("[dj] interject: %v", err)
			}
		}
		if batchHasFallback {
			activeNewsFallbacks.Add(-1)
		}
	}
	return nil
}

func mayInterruptForNews(mode string, clock *news.ProgramClock, now time.Time) bool {
	if mode == "news" {
		return true
	}
	if mode != "radio" || clock == nil {
		return false
	}
	_, due := clock.Due(now)
	return due
}

func segmentsContainNews(segs []Segment) bool {
	for _, seg := range segs {
		if seg.IsNews {
			return true
		}
	}
	return false
}

func segmentsContainNewsFallback(segs []Segment) bool {
	for _, seg := range segs {
		if seg.NewsFallback {
			return true
		}
	}
	return false
}

func cleanupTransient(tempDir, path string) {
	if strings.TrimSpace(tempDir) == "" || strings.TrimSpace(path) == "" {
		return
	}
	root, err := filepath.Abs(tempDir)
	if err != nil {
		return
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." {
		return
	}
	if strings.EqualFold(filepath.Ext(target), ".mp3") {
		_ = os.Remove(target)
		_ = os.Remove(strings.TrimSuffix(target, filepath.Ext(target)) + ".license.json")
	}
}

// buildTanda returns the ordered segments for one batch (requested songs
// first, then fresh picks), with DJ voice intros interleaved. Voices are
// generated here (GLM+qohl) — called by the producer ahead of playback.
func buildTanda(cfg config.Config, lib library.Library, djx *dj.DJ, vox *voice.Voice, pool *skills.Pool, st *status.Server, mode string, newsPrep *newsPreloader, programClock *news.ProgramClock, trackCount *int) (segs []Segment, reqs string, err error) {
	addVoice := func(text string, midroll bool) {
		if !cfg.DJEnabled || vox == nil || strings.TrimSpace(text) == "" {
			return
		}
		if vf, verr := vox.Speak(text); verr == nil {
			segs = append(segs, Segment{Path: vf, IsVoice: true, Text: text, Midroll: midroll})
			log.Printf("[dj] %s", text) // stderr (debug); the air-time log fires in the consumer
		} else {
			log.Printf("[dj] voice: %v", verr)
		}
	}
	addTrack := func(t library.Track) {
		segs = append(segs, Segment{Path: t.Src, Meta: t})
	}
	addNews := func(slot *news.ProgramSlot) bool {
		prepared, ok := newsPrep.tryTake(slot)
		if !ok {
			return false
		}
		if slot != nil {
			for i := range prepared {
				if prepared[i].IsNews {
					prepared[i].Program = slot
					break
				}
			}
		}
		segs = append(segs, prepared...)
		return true
	}
	if mode == "news" {
		if addNews(nil) {
			return segs, "", nil
		}
		// News-only mode must never become a silence loop when the background
		// renderer is still working or feeds are temporarily quiet. Music is a
		// safe filler and the next producer cycle checks the ready queue again.
		if t, e := lib.Next(); e == nil {
			log.Printf("[news] no READY bulletin — music fallback")
			segs = append(segs, Segment{Path: t.Src, Meta: t, NewsFallback: true})
			*trackCount++
			return segs, "", nil
		}
		return nil, "", fmt.Errorf("news mode: no bulletin and no music fallback")
	}
	if mode == "music" {
		for i := 0; i < cfg.Chunk; i++ {
			if t, e := lib.Next(); e == nil {
				addTrack(t)
				*trackCount++
			}
		}
		return segs, "", nil
	}

	// Radio mode follows the wall clock. News is prepared in the background and
	// airs at the next natural song boundary only when a formal slot is due.
	didNews := false
	newsDue := false
	if cfg.DJEnabled && len(cfg.NewsFeeds) > 0 {
		if slot, due := programClock.Due(time.Now()); due {
			newsDue = true
			didNews = addNews(&slot)
		}
	}

	matched := 0
	var reqCtx []dj.Req
	for _, req := range st.DrainRequests() {
		ms, _ := lib.Search(req.Text)
		if len(ms) > 0 {
			t := ms[0]
			if cfg.DJEnabled {
				addVoice(skills.RequestAck(djx, t, req.From, req.Text), false)
			}
			addTrack(t)
			lib.MarkPlayed(t.Src)
			reqCtx = append(reqCtx, dj.Req{From: req.From, Query: req.Text, Title: t.Title, Artist: t.Artist})
			matched++
			who := req.Text
			if req.From != "" {
				who = req.From + ": " + req.Text
			}
			log.Printf("[request] %q → %s — %s", who, t.Title, t.Artist)
			segs[len(segs)-1].Req = fmt.Sprintf("%q → %s — %s", who, t.Title, t.Artist)
		} else {
			log.Printf("[request] no match %q", req.Text)
		}
	}

	// Radio mode always returns from a news article through an explicit song
	// introduction. This is kept out of the news-continuous branch above, where
	// the requested flow is article → DJ discussion → next article.
	if didNews {
		// A matched request already supplied its own spoken introduction + song.
		if matched == 0 {
			if t, e := lib.Next(); e == nil {
				if cfg.DJEnabled && djx != nil {
					addVoice(djx.Banter(t.Title, t.Artist, t.Album), false)
				}
				addTrack(t)
				*trackCount++
			}
		}
		if len(segs) == 0 {
			return nil, "", fmt.Errorf("news: no music after bulletin")
		}
		return segs, reqs, nil
	}
	if mode == "radio" {
		// News is still rendering. Keep one song on air, then check again; do
		// not pre-plan an eight-song block that delays a newly READY bulletin.
		if len(segs) == 0 {
			if t, e := lib.Next(); e == nil {
				segs = append(segs, Segment{Path: t.Src, Meta: t, NewsFallback: newsDue})
				*trackCount++
			}
		}
		if len(segs) == 0 {
			return nil, "", fmt.Errorf("radio: no news and no music fallback")
		}
		return segs, reqs, nil
	}

	// DJ Director: one structured GLM call plans the whole setlist + talk breaks.
	// The LLM picks+orders a coherent arc from a shortlist and decides WHEN to
	// talk (intro/trivia/wiki/history/time/none), modulated by cfg.DJTalk.
	// On any failure → random fallback so the station never stops.
	// A local reasoning model can take longer than Icecast's initial-source
	// window to create a full structured plan. Ollama therefore skips the JSON
	// planner, but still gets short plain-text DJ breaks that are cheap enough to
	// prefetch while the current music is already on air.
	if cfg.DJEnabled && djx != nil && !strings.EqualFold(cfg.LLMProvider, "ollama") {
		cands := lib.Sample(12)
		if len(cands) > 0 {
			ctx := dj.Ctx{
				Talk:       cfg.DJTalk,
				TimeOfDay:  timeOfDay(time.Now()),
				History:    histCands(st.Current().History),
				Candidates: libCands(cands),
				Requests:   reqCtx,
			}
			if plan, perr := djx.DirectPlan(ctx); perr == nil {
				bm := map[int][]dj.Break{}
				for _, b := range plan.Breaks {
					bm[b.Before] = append(bm[b.Before], b)
				}
				for pos, id := range plan.Setlist {
					for _, b := range bm[pos] {
						switch {
						case b.Kind == "time":
							segs = append(segs, Segment{IsVoice: true, LiveTime: true})
						case b.Kind == "none" || b.Kind == "":
							// skip
						case b.At == "mid":
							addVoice(djx.SayMidroll(cands[id].Title, cands[id].Artist), true)
						case b.Kind == "wiki":
							addVoice(djx.SayWiki(cands[id].Artist, cands[id].Title), false)
						default:
							addVoice(djx.Banter(cands[id].Title, cands[id].Artist, cands[id].Album), false)
						}
					}
					addTrack(cands[id])
					*trackCount++
				}
				// commit only the chosen tracks; the rest stay available next tanda
				for _, id := range plan.Setlist {
					lib.MarkPlayed(cands[id].Src)
				}
			} else {
				log.Printf("[dj] director falló (%v) — random fallback", perr)
				for i := 0; i < cfg.Chunk; i++ {
					if t, e := lib.Next(); e == nil {
						if i == 0 && !didNews {
							// Keep the AI DJ on air even when the structured planner
							// rejects a plan; the music fallback remains non-blocking.
							addVoice(djx.Banter(t.Title, t.Artist, t.Album), false)
						}
						addTrack(t)
						*trackCount++
					}
				}
			}
		}
	} else {
		for i := 0; i < cfg.Chunk; i++ {
			if t, e := lib.Next(); e == nil {
				// Never put a local Ollama request on the playback producer's
				// critical path. A reasoning model may take tens of seconds before
				// returning its first token; music must start immediately. Ollama
				// commentary is prepared by newsPreloader in the background instead.
				shouldTalk := cfg.DJEnabled && djx != nil && !strings.EqualFold(cfg.LLMProvider, "ollama") && cfg.DJEvery > 0 && (*trackCount == 0 || *trackCount%cfg.DJEvery == 0)
				// The preloaded news break already includes its grounded DJ
				// reaction; avoid stacking another intro immediately after it.
				if shouldTalk && !(didNews && i == 0) {
					addVoice(djx.Banter(t.Title, t.Artist, t.Album), false)
				}
				addTrack(t)
				*trackCount++
			}
		}
	}

	if len(segs) == 0 {
		return nil, "", fmt.Errorf("no segments")
	}
	if matched > 0 {
		reqs = fmt.Sprintf(", %d pedido(s)", matched)
	}
	return segs, reqs, nil
}

func nextTrack(segs []Segment, from int) library.Track {
	for j := from + 1; j < len(segs); j++ {
		if segs[j].IsVoice || segs[j].IsNews || segs[j].IsDJ {
			continue
		}
		return segs[j].Meta
	}
	return library.Track{}
}

func toStatus(t library.Track) status.Track {
	d := 0.0
	// Duration only for local files — ffprobe on a Navidrome stream URL is a
	// slow network probe we don't want on every SetCurrent.
	if t.Src != "" && !strings.Contains(t.Src, "://") {
		d = library.Duration(t.Src).Seconds()
	}
	return status.Track{Type: "music", Title: t.Title, Artist: t.Artist, Album: t.Album, Year: t.Year, BPM: t.BPM, Duration: d, Src: t.Src, AttributionURL: t.AttributionURL, LicenseURL: t.LicenseURL}
}

func toNewsStatus(item news.Item, speech string) status.Track {
	return status.Track{Type: "news", Title: item.Title, Source: item.Source, Category: item.Category, URL: item.URL, Description: item.Description, PublishedAt: item.PublishedAt, ImageURL: item.ImageURL, SpeechText: speech}
}

func or(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// libCands / histCands map library + status tracks into the director's Cand
// shape (the director only knows its own types — dj is decoupled from
// library/status to avoid import cycles).
func libCands(ts []library.Track) []dj.Cand {
	out := make([]dj.Cand, len(ts))
	for i, t := range ts {
		out[i] = dj.Cand{ID: i, Title: t.Title, Artist: t.Artist, Album: t.Album}
	}
	return out
}

func toNewsFeeds(feeds []config.NewsFeed) []news.Feed {
	out := make([]news.Feed, len(feeds))
	for i, f := range feeds {
		out[i] = news.Feed{Name: f.Name, URL: f.URL, Category: f.Category}
	}
	return out
}

func histCands(ts []status.Track) []dj.Cand {
	out := make([]dj.Cand, len(ts))
	for i, t := range ts {
		out[i] = dj.Cand{Title: t.Title, Artist: t.Artist, Album: t.Album}
	}
	return out
}

// timeOfDay returns a coarse ES time-of-day tag for the director's context.
func timeOfDay(t time.Time) string {
	switch h := t.Hour(); {
	case h < 6:
		return "madrugada"
	case h < 12:
		return "mañana"
	case h < 19:
		return "tarde"
	default:
		return "noche"
	}
}
