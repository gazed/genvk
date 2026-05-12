// SPDX-FileCopyrightText : © 2026 Galvanized Logic Inc.
// SPDX-License-Identifier: MIT

package main

// vulkan vk.xml specification parser based on a (rough/partial)
// golang port of the official pythyon spec parser scripts. See:
// https://github.com/KhronosGroup/Vulkan-Docs + /scripts/genvk.py

import (
	"flag"
	"io"
	"io/fs"
	"io/ioutil"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	// golang xml parsing similar to python etree.
	"github.com/beevik/etree" // godoc: https://pkg.go.dev/github.com/beevik/etree
)

var (
	// flags
	vulkanSpec      string // vulkan spec.
	bindingDir      string // output directory for vulkan bindings.
	vulkanAPI       string // which vulkan spec
	vulkanPlatforms string // desired vulkan platforms as comma separated string.

	// parsed from vulkanPlatforms.
	platforms []string // desired vulkan platforms as string slice.
)

// init is run, automatically by go, once on startup.
func init() {
	flag.StringVar(&vulkanSpec, "spec", "vk.xml", "Vulkan XML registry file to read")
	flag.StringVar(&bindingDir, "outdir", "vk", "Directory to write generated bindings")
	flag.StringVar(&vulkanAPI, "api", "vulkan", "API to generate against; ie: 'vulkan' and 'vulkansc'")
	flag.StringVar(&vulkanPlatforms, "platforms", "win32,metal", "Comma-separated list of vulkan platforms to generate")
	flag.Parse()
	platforms = strings.Split(vulkanPlatforms, ",")
}

// setLogging keeps the default logging settings.
// Overridden by debug builds.
var setLogging func(w io.Writer) = func(w io.Writer) {
	slog.SetDefault(slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo})))
}

// main is the standard startup execution entry point.
func main() {
	setLogging(os.Stdout)
	createOutputDirectory(bindingDir)

	// open the vulkan xml spec as an xml etree document.
	tree := etree.NewDocument()
	if err := tree.ReadFromFile(vulkanSpec); err != nil {
		slog.Error("Could not open Vulkan xml spec", "filename", vulkanSpec, "error", err)
		return
	}

	// based on the python script Vulkan_Docs/scripts/reg.py
	// NOTE: only tested using the "vulkan" api
	reg := NewRegistry(tree)
	reg.parseTree(vulkanAPI)

	// based on the python script Vulkan_Docs/scripts/generator.py
	reg.gen = newGenerator()
	reg.apiGen(vulkanAPI)

	// add the static files to the generated code.
	copyStaticFiles("static", bindingDir)
}

// find or create the output directory.
func createOutputDirectory(outDir string) {
	_, err := os.Stat(outDir)
	if err != nil {
		if os.IsNotExist(err) {
			if err := os.Mkdir(outDir, 0777|fs.ModeDir); err != nil {
				slog.Error("Could not create output directory", "dir", outDir, "error", err)
				return
			} else {
				slog.Info("Output directory created", "dir", outDir)
			}
		}
	}
}

// copyStaticFiles copies static files to the output directory.
func copyStaticFiles(staticDir, outDir string) {
	files := []string{
		"static.go",
		"static_cgo.go",
		"static_syscall.go",
	}
	for _, f := range files {
		data, err := ioutil.ReadFile(filepath.Join(staticDir, f))
		if err != nil {
			slog.Error("failed to read static file", "file", f, "error", err)
		}
		err = ioutil.WriteFile(filepath.Join(outDir, f), data, 0666)
		if err != nil {
			slog.Error("failed to write static file", "file", f, "error", err)
		}
	}
}
