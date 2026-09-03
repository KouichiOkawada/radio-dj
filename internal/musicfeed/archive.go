// Package musicfeed maintains a tiny disposable pool of Creative Commons
// vocal songs from ccMixter, a music-specific open-license community.
package musicfeed

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type ccMixterTrack struct {
	ID         int    `json:"upload_id"`
	Title      string `json:"upload_name"`
	Artist     string `json:"user_name"`
	PageURL    string `json:"file_page_url"`
	LicenseURL string `json:"license_url"`
	Tags       string `json:"upload_tags"`
	Files      []struct {
		DownloadURL string `json:"download_url"`
		RawSize     int64  `json:"file_rawsize"`
		Format      struct {
			MIME string `json:"mime_type"`
		} `json:"file_format_info"`
	} `json:"files"`
}

type history struct {
	Seen map[string]time.Time `json:"seen"`
}

func Start(ctx context.Context, tempDir, stateDir string) {
	if strings.TrimSpace(tempDir) == "" {
		return
	}
	_ = os.MkdirAll(tempDir, 0o755)
	go func() {
		fillPool(ctx, tempDir, stateDir)
		// Refill promptly after the player removes a one-shot track. A fill is a
		// cheap count check when the pool already has two songs.
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				fillPool(ctx, tempDir, stateDir)
			}
		}
	}()
}

func fillPool(ctx context.Context, tempDir, stateDir string) {
	for countMP3(tempDir) < 2 {
		before := countMP3(tempDir)
		fill(ctx, tempDir, stateDir)
		if countMP3(tempDir) <= before {
			return
		}
	}
}

func fill(ctx context.Context, tempDir, stateDir string) {
	historyPath := filepath.Join(stateDir, "auto-music-history.json")
	h := loadHistory(historyPath)
	client := &http.Client{Timeout: 90 * time.Second}
	tracks, err := query(ctx, client)
	if err != nil {
		log.Printf("[musicfeed] ccMixter unavailable: %v", err)
		return
	}
	for _, track := range tracks {
		key := fmt.Sprint(track.ID)
		if !h.Seen[key].IsZero() || !allowedLicense(track.LicenseURL) || !isCompleteVocalMix(track.Tags) {
			continue
		}
		if err := download(ctx, client, track, tempDir); err == nil {
			h.Seen[key] = time.Now()
			saveHistory(historyPath, h)
			log.Printf("[musicfeed] ready: %s — %s (%s)", track.Title, track.Artist, track.LicenseURL)
			return
		} else {
			log.Printf("[musicfeed] download failed for %q: %v", track.Title, err)
		}
	}
}

func query(ctx context.Context, client *http.Client) ([]ccMixterTrack, error) {
	// This ccMixter installation currently caps JSON queries to one result and
	// ignores offset. Fetch the official ID view first, then resolve a shuffled
	// handful individually. Pop-tagged vocal mixes are tried first; the much
	// larger vocal-remix set remains a fallback because ccMixter currently has
	// no reusable J-pop results under this license filter.
	var ids []string
	seenIDs := map[string]bool{}
	for _, tags := range []string{"remix+female_vocals+pop", "remix+female_vocals"} {
		found, err := queryIDs(ctx, client, tags)
		if err != nil && len(ids) == 0 {
			return nil, err
		}
		rand.Shuffle(len(found), func(i, j int) { found[i], found[j] = found[j], found[i] })
		if len(found) > 6 {
			found = found[:6]
		}
		for _, id := range found {
			id = strings.TrimSpace(id)
			if id != "" && !seenIDs[id] {
				seenIDs[id] = true
				ids = append(ids, id)
			}
		}
	}
	var tracks []ccMixterTrack
	for _, id := range ids {
		found, err := queryTrack(ctx, client, id)
		if err == nil {
			tracks = append(tracks, found...)
		}
	}
	return tracks, nil
}

func queryIDs(ctx context.Context, client *http.Client, tags string) ([]string, error) {
	idsURL, _ := url.Parse("https://ccmixter.org/api/query")
	idsQuery := idsURL.Query()
	idsQuery.Set("f", "ids")
	idsQuery.Set("tags", tags)
	idsQuery.Set("type", "all")
	idsQuery.Set("lic", "by")
	idsQuery.Set("limit", "query")
	idsURL.RawQuery = idsQuery.Encode()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, idsURL.String(), nil)
	req.Header.Set("User-Agent", "radio-dj/1.0 (+personal-radio)")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("ID query HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	idText := strings.Trim(strings.TrimSpace(string(body)), "[]")
	ids := strings.FieldsFunc(idText, func(r rune) bool { return r == ';' || r == ',' || r == '\n' || r == '\r' })
	if len(ids) == 0 {
		return nil, fmt.Errorf("ID query returned no tracks")
	}
	return ids, nil
}

func queryTrack(ctx context.Context, client *http.Client, id string) ([]ccMixterTrack, error) {
	u, _ := url.Parse("https://ccmixter.org/api/query")
	q := u.Query()
	q.Set("f", "json")
	q.Set("dataview", "info")
	q.Set("ids", id)
	u.RawQuery = q.Encode()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	req.Header.Set("User-Agent", "radio-dj/1.0 (+personal-radio)")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var tracks []ccMixterTrack
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&tracks); err != nil {
		return nil, err
	}
	return tracks, nil
}

func download(ctx context.Context, client *http.Client, track ccMixterTrack, tempDir string) error {
	var fileURL string
	for _, file := range track.Files {
		if file.RawSize >= 1<<20 && file.RawSize <= 35<<20 &&
			(strings.EqualFold(file.Format.MIME, "audio/mpeg") || strings.HasSuffix(strings.ToLower(file.DownloadURL), ".mp3")) {
			fileURL = file.DownloadURL
			break
		}
	}
	if fileURL == "" {
		return fmt.Errorf("no suitable MP3")
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	// ccMixter's content host rejects direct hotlinks without a browser-like
	// agent and a ccMixter page referrer (HTTP 403).
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; radio-dj/1.0; personal-radio)")
	req.Header.Set("Referer", track.PageURL)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download HTTP %d", resp.StatusCode)
	}
	base := safeName(track.Title + "__" + track.Artist + "__ccmixter-" + fmt.Sprint(track.ID))
	part := filepath.Join(tempDir, base+".part")
	final := filepath.Join(tempDir, base+".mp3")
	out, err := os.OpenFile(part, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(out, io.LimitReader(resp.Body, 36<<20))
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil || written < 1<<20 || written >= 36<<20 {
		_ = os.Remove(part)
		return fmt.Errorf("invalid download")
	}
	if err := os.Rename(part, final); err != nil {
		_ = os.Remove(part)
		return err
	}
	attribution := map[string]string{"title": track.Title, "creator": track.Artist, "source": track.PageURL, "license": track.LicenseURL}
	if data, err := json.MarshalIndent(attribution, "", "  "); err == nil {
		_ = os.WriteFile(strings.TrimSuffix(final, ".mp3")+".license.json", data, 0o644)
	}
	return nil
}

func allowedLicense(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.Contains(value, "creativecommons.org/licenses/by/") ||
		strings.Contains(value, "creativecommons.org/licenses/by-sa/") ||
		strings.Contains(value, "creativecommons.org/publicdomain/zero/")
}

func isCompleteVocalMix(tags string) bool {
	tags = "," + strings.ToLower(strings.Trim(tags, ",")) + ","
	return strings.Contains(tags, ",remix,") && strings.Contains(tags, ",female_vocals,") &&
		!strings.Contains(tags, ",acappella,") && !strings.Contains(tags, ",sample,")
}

func safeName(value string) string {
	replacer := strings.NewReplacer("<", "_", ">", "_", ":", "_", "\"", "_", "/", "_", "\\", "_", "|", "_", "?", "_", "*", "_")
	value = strings.TrimSpace(replacer.Replace(value))
	runes := []rune(value)
	if len(runes) > 120 {
		value = string(runes[:120])
	}
	if value == "" {
		return fmt.Sprintf("open-music-%d", time.Now().Unix())
	}
	return value
}

func countMP3(dir string) int {
	entries, _ := os.ReadDir(dir)
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".mp3") {
			count++
		}
	}
	return count
}

func loadHistory(path string) history {
	h := history{Seen: map[string]time.Time{}}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &h)
	}
	if h.Seen == nil {
		h.Seen = map[string]time.Time{}
	}
	return h
}

func saveHistory(path string, h history) {
	if data, err := json.MarshalIndent(h, "", "  "); err == nil {
		_ = os.WriteFile(path, data, 0o644)
	}
}
