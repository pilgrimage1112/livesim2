// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"encoding/json"
	"fmt"
	"net/http"

	//"os"
	"strconv"
	//"bufio"
	//"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Dash-Industry-Forum/livesim2/pkg/logging"

	htmpl "html/template"
	ttmpl "text/template"
)

type Server struct {
	Router        *chi.Mux
	LiveRouter    *chi.Mux
	VodRouter     *chi.Mux
	logger        *logging.Logger
	Cfg           *ServerConfig
	assetMgr      *assetMgr
	textTemplates *ttmpl.Template
	htmlTemplates *htmpl.Template
	InitTime      int64
	//latencyTrace  map[float64]int
}

// func (s *Server) initLatencyTrace() {
// 	file, err := os.Open("trace.csv")
// 	if err != nil {
// 		fmt.Println("Error opening file:", err)
// 		return
// 	}
// 	defer file.Close()

// 	scanner := bufio.NewScanner(file)
//     count := 0
//     for scanner.Scan() {
// 		if count == 0 {
//             count++
//             continue
//         }
// 		if count > 33474 {
// 			break
// 		}
//         line := scanner.Text()
//         parts := strings.Split(line, ",")
//         since, _ := strconv.ParseFloat(parts[0], 64)
//         rtt, _ := strconv.ParseFloat(parts[2], 64)
//         s.latencyTrace[since] = int(rtt)
//         count++
//     }

// }

func (s *Server) healthzHandlerFunc(w http.ResponseWriter, r *http.Request) {
	s.jsonResponse(w, true, http.StatusOK)
}

func (s *Server) GetLogger() *logging.Logger {
	return s.logger
}

// jsonResponse marshals message and give response with code
//
// Don't add any more content after this since Content-Length is set
func (s *Server) jsonResponse(w http.ResponseWriter, message interface{}, code int) {
	raw, err := json.Marshal(message)
	if err != nil {
		http.Error(w, fmt.Sprintf("{message: \"%s\"}", err), http.StatusInternalServerError)
		s.logger.Error().Msg(err.Error())
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
	_, err = w.Write(raw)
	if err != nil {
		s.logger.Error().
			Str("error", err.Error()).
			Msg("Could not write HTTP response")
	}
}

func (s *Server) compileTemplates() error {
	var err error
	s.textTemplates, err = compileTextTemplates(content, "templates")
	if err != nil {
		return fmt.Errorf("compileTextTemplates: %w", err)
	}
	s.logger.Debug().Str("tmpl", s.textTemplates.DefinedTemplates()).Msg("text templates")
	s.htmlTemplates, err = compileHTMLTemplates(content, "templates")
	if err != nil {
		return fmt.Errorf("compileHTMLTemplates: %w", err)
	}
	s.logger.Debug().Str("tmpl", s.htmlTemplates.DefinedTemplates()).Msg("html templates")

	return nil
}
