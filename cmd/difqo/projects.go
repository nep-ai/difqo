package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/nep-ai/difqo/internal/store"
)

var projectID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

func loadProjects(path string) ([]store.SpecialistProject, error) {
	if path == "" {
		return nil, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("could not open the local project catalog")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil || len(data) > 64<<10 {
		return nil, errors.New("could not read the local project catalog within 64 KiB")
	}
	var entries []struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Checkout string `json:"checkout"`
		Version  string `json:"binding_version"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&entries); err != nil || entries == nil || decoder.Decode(new(any)) != io.EOF || len(entries) > 32 {
		return nil, errors.New("project catalog must be a JSON array of up to 32 project bindings")
	}

	projects := make([]store.SpecialistProject, 0, len(entries))
	ids := make(map[string]bool)
	paths := make(map[string]bool)
	for i, entry := range entries {
		name := strings.TrimSpace(entry.Name)
		version := strings.TrimSpace(entry.Version)
		if !projectID.MatchString(entry.ID) || ids[entry.ID] || name == "" || len(name) > 160 ||
			version == "" || len(version) > 128 || !filepath.IsAbs(entry.Checkout) {
			return nil, fmt.Errorf("invalid or duplicate project binding at catalog entry %d", i+1)
		}
		checkout, err := filepath.EvalSymlinks(entry.Checkout)
		if err != nil {
			return nil, fmt.Errorf("could not resolve checkout at catalog entry %d", i+1)
		}
		root, err := exec.Command("git", "-C", checkout, "rev-parse", "--show-toplevel").Output()
		if err != nil || strings.TrimSuffix(string(root), "\n") != checkout || paths[checkout] {
			return nil, fmt.Errorf("catalog entry %d requires a distinct Git checkout root", i+1)
		}
		ids[entry.ID] = true
		paths[checkout] = true
		binding := sha256.Sum256([]byte(checkout + "\x00" + version))
		projects = append(projects, store.SpecialistProject{ID: entry.ID, Name: name, Label: filepath.Base(checkout) + " · " + version, Binding: hex.EncodeToString(binding[:])})
	}
	return projects, nil
}
