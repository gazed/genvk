// SPDX-FileCopyrightText : © 2026 Galvanized Logic Inc.
// SPDX-License-Identifier: MIT

//go:build debug

package main

// main_debug.go turns on structured logging debug logs
// when building with "go build -tags debug"

import (
	"io"
	"log/slog"
	"os"
)

// override the default setLogging to dump debugging logs directly
// to the console.
func init() {
	setLogging = func(w io.Writer) {
		// LevelDebug is used to find loading and startup issues.
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
	}
}
