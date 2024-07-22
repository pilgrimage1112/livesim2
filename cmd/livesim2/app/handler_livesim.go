// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/Dash-Industry-Forum/livesim2/pkg/logging"
	"github.com/rs/zerolog"
)

func (s *Server) ifHandoverPeriod() bool {
	handovermoment := [...]int{12, 27, 42, 57, 72, 87, 102, 117, 132, 147, 162, 177, 192, 207, 222, 237, 252, 267, 282, 297, 312, 327, 342, 357, 373}
	nowTime := int64(time.Now().UnixMilli())
	period := (nowTime - s.InitTime) / 1000
	for i := 0; i < len(handovermoment); i++ {
		if period > int64(handovermoment[i]-1) && period < int64(handovermoment[i]+1) {
			return true
		}
	}
	return false
}

// livesimHandlerFunc handles mpd and segment requests.
// ?nowMS=... can be used to set the current time for testing.
func (s *Server) livesimHandlerFunc(w http.ResponseWriter, r *http.Request) {
	log := logging.SubLoggerWithRequestIDAndTopic(r, "livesim")
	uPath := r.URL.Path
	ext := filepath.Ext(uPath)
	u, err := url.Parse(uPath)
	if err != nil {
		msg := "URL parser"
		log.Error().Err(err).Msg(msg)
		http.Error(w, msg, http.StatusInternalServerError)
		return
	}

	var nowMS int // Set from query string or from wall-clock
	q := r.URL.Query()
	nowMSValue := q.Get("nowMS")
	if nowMSValue != "" {
		nowMS, err = strconv.Atoi(nowMSValue)
		if err != nil {
			http.Error(w, "bad nowMS query", http.StatusBadRequest)
			return
		}
	} else {
		nowMS = int(time.Now().UnixMilli())
	}

	cfg, err := processURLCfg(u.String(), nowMS)
	if err != nil {
		msg := "processURL error"
		log.Error().Err(err).Msg(msg)
		http.Error(w, msg, http.StatusInternalServerError)
		return
	}
	contentPart := cfg.URLContentPart()
	log.Debug().Str("url", contentPart).Msg("requested content")
	a, ok := s.assetMgr.findAsset(contentPart)
	if !ok {
		http.Error(w, "unknown asset", http.StatusNotFound)
		return
	}
	if nowMS < cfg.StartTimeS*1000 {
		tooEarlyMS := cfg.StartTimeS - nowMS
		http.Error(w, fmt.Sprintf("%dms too early", tooEarlyMS), http.StatusTooEarly)
		return
	}
	switch ext {
	case ".mpd":
		_, mpdName := path.Split(contentPart)
		cfg.SetScheme(s.Cfg.Scheme, r)
		cfg.SetHost(s.Cfg.Host, r)
		err := writeLiveMPD(log, w, cfg, a, mpdName, nowMS)
		if err != nil {
			// TODO. Add more granular errors like 404 not found
			msg := fmt.Sprintf("liveMPD: %s", err)
			log.Error().Err(err).Msg(msg)
			http.Error(w, msg, http.StatusInternalServerError)
			return
		}
	case ".mp4", ".m4s", ".cmfv", "cmfa", "cmft":
		segmentPart := strings.TrimPrefix(contentPart, a.AssetPath) // includes heading /
		// nowTime := int64(time.Now().UnixMilli())
		// if nowTime-s.InitTime > 12000 {
		// 	cfg.DynamicChunkFlag = false
		// 	log.Info().Msg("ChunkMode start!")
		// }
		dynamicMode := false
		if cfg.DynamicChunkFlag {
			if s.ifHandoverPeriod() {
				dynamicMode = true
				log.Info().Msg("ChunkMode Turn on !")

			} else {
				dynamicMode = false
				log.Info().Msg("ChunkMode Trun off !")
			}
		}
		err = writeSegment(r.Context(), w, log, cfg, s.assetMgr.vodFS, a, segmentPart[1:], nowMS, s.textTemplates, dynamicMode)
		if err != nil {
			var tooEarly errTooEarly
			switch {
			case errors.Is(err, errNotFound):
				http.Error(w, "Not Found", http.StatusNotFound)
				return
			case errors.As(err, &tooEarly):
				http.Error(w, tooEarly.Error(), http.StatusTooEarly)
			default:
				msg := "writeSegment"
				log.Error().Err(err).Msg(msg)
				http.Error(w, msg, http.StatusInternalServerError)
				return
			}
		}
	default:
		http.Error(w, "unknown file extension", http.StatusNotFound)
		return
	}
}

func writeLiveMPD(log *zerolog.Logger, w http.ResponseWriter, cfg *ResponseConfig, a *asset, mpdName string, nowMS int) error {
	work := make([]byte, 0, 1024)
	buf := bytes.NewBuffer(work)
	lMPD, err := LiveMPD(a, mpdName, cfg, nowMS)
	if err != nil {
		return fmt.Errorf("convertToLive: %w", err)
	}
	size, err := lMPD.Write(buf, "  ", true)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Length", strconv.Itoa(size))
	w.Header().Set("Content-Type", "application/dash+xml")
	n, err := w.Write(buf.Bytes())
	if err != nil {
		log.Error().Err(err).Msg("writing response")
		return err
	}
	if n != size {
		log.Error().Int("size", size).Int("nr written", n).Msg("could not write all bytes")
		return err
	}
	return nil
}

func writeSegment(ctx context.Context, w http.ResponseWriter, log *zerolog.Logger, cfg *ResponseConfig, vodFS fs.FS, a *asset,
	segmentPart string, nowMS int, tt *template.Template, dynamicMode bool) error {
	// First check if init segment and return
	isInitSegment, err := writeInitSegment(w, cfg, vodFS, a, segmentPart)
	if err != nil {
		return fmt.Errorf("writeInitSegment: %w", err)
	}
	if isInitSegment {
		return nil
	}
	if !cfg.AvailabilityTimeCompleteFlag && dynamicMode { //!!!!!
		return writeChunkedSegment(ctx, w, log, cfg, vodFS, a, segmentPart, nowMS)
	}
	return writeLiveSegment(w, cfg, vodFS, a, segmentPart, nowMS, tt)
	// Chunked low-latency mode
}
