// Command radio-dj is a tiny 24/7 personal radio DJ.
//
//	serve                 run the station (picks tracks, talks between them, streams to Icecast)
//	now                   print what's on air
//	download <url>        (stub) fetch new music — jarasch vs radio-dj TBD
//
// Config is env-only (RDJ_*). Folder mode runs with zero config.
package main

import (
	"fmt"
	"os"

	"radio-dj/internal/config"
	"radio-dj/internal/install"
	"radio-dj/internal/radio"
)

func main() {
	cfg := config.Load()
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		if err := radio.Serve(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "radio-dj: %v\n", err)
			os.Exit(1)
		}
	case "now":
		radio.PrintNow(cfg)
	case "install":
		if err := install.Install(""); err != nil {
			fmt.Fprintf(os.Stderr, "install: %v\n", err)
			os.Exit(1)
		}
	case "uninstall":
		if err := install.Uninstall(); err != nil {
			fmt.Fprintf(os.Stderr, "uninstall: %v\n", err)
			os.Exit(1)
		}
	case "download":
		// Deferred: decide jarasch (yt-dlp engine) vs a built-in downloader.
		fmt.Println("download: TBD — pending jarasch vs radio-dj decision")
		os.Exit(0)
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Println(`radio-dj — tiny 24/7 personal DJ radio

commands:
  serve              pick tracks, talk between them, stream to Icecast
  now                what's on air right now
  download <url>     (stub) fetch new music

config (env, RDJ_*):
  RDJ_ICECAST_SOURCE_PW   required to stream (icecast source password)
  RDJ_LIBRARY             folder of music (default ~/Music/library)
  RDJ_SOURCE              folder | navidrome (default folder)
  RDJ_NAVIDROME_URL/USER/PASS   for navidrome source
  RDJ_GLM_API_KEY         enables the DJ (GLM-5.2 over OpenAI-compatible API)
  RDJ_VOICE_CMD            TTS command template, e.g. "qohl speak {text} -o {out}"
  RDJ_DJ_EVERY            voice an intro every N tracks (default 3)
  RDJ_BITRATE             MP3 bitrate kbps (default 192)

When RDJ_GLM_API_KEY or RDJ_VOICE_CMD is unset the station runs music-only.`)
}
