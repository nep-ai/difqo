package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/nep-ai/difqo/internal/store"
)

type projectCatalog struct {
	mu       sync.RWMutex
	path     string
	projects []store.SpecialistProject
}

func openProjectCatalog(managedSetup, path string) (*projectCatalog, error) {
	catalog := &projectCatalog{}
	if managedSetup != "" {
		managed := filepath.Join(managedSetup, "projects.json")
		if path == "" || path == managed {
			catalog.path, path = managed, managed
			info, err := os.Lstat(managed)
			if errors.Is(err, os.ErrNotExist) {
				return catalog, nil
			}
			if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
				return nil, errors.New("the managed project catalog must be a private regular file")
			}
		}
	}
	projects, err := loadProjects(path)
	if err != nil {
		return nil, err
	}
	catalog.projects = projects
	return catalog, nil
}

func (c *projectCatalog) list() []store.SpecialistProject {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return slices.Clone(c.projects)
}

func (c *projectCatalog) add(name, path, check string) (store.SpecialistProject, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.path == "" {
		return store.SpecialistProject{}, errors.New("This service uses a separately managed project catalog.")
	}
	if info, err := os.Lstat(c.path); err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			return store.SpecialistProject{}, errors.New("The project catalog must be a private regular file.")
		}
		saved, err := loadProjects(c.path)
		if err != nil || !slices.Equal(saved, c.projects) {
			return store.SpecialistProject{}, errors.New("The project catalog changed on disk. Restart Difqo before adding another project.")
		}
	} else if !errors.Is(err, os.ErrNotExist) || len(c.projects) > 0 {
		return store.SpecialistProject{}, errors.New("Couldn’t read the private project catalog.")
	}
	if len(c.projects) >= 32 {
		return store.SpecialistProject{}, errors.New("This pilot supports up to 32 local projects.")
	}
	name, path, check = strings.TrimSpace(name), strings.TrimSpace(path), strings.TrimSpace(check)
	if check == "" {
		return store.SpecialistProject{}, errors.New("Enter a check command you trust for this repository.")
	}
	var key [12]byte
	rand.Read(key[:])
	entry := projectEntry{ID: "local-" + hex.EncodeToString(key[:]), Name: name, Checkout: path, Version: "1", Check: check}
	validated, err := validateProjects([]projectEntry{entry})
	if err != nil {
		return store.SpecialistProject{}, errors.New("Enter a project name, the absolute path to a Git checkout, and a check command of up to 4,096 characters.")
	}
	project := validated[0]
	entries := make([]projectEntry, 0, len(c.projects)+1)
	for _, current := range c.projects {
		if current.Checkout == project.Checkout {
			return store.SpecialistProject{}, errors.New("That checkout is already registered.")
		}
		entries = append(entries, projectEntry{ID: current.ID, Name: current.Name, Checkout: current.Checkout, Version: current.Version, Check: current.Check})
	}
	entry.Checkout = project.Checkout
	entries = append(entries, entry)
	if err := saveProjects(c.path, entries); err != nil {
		return store.SpecialistProject{}, errors.New("Couldn’t save the private project catalog.")
	}
	c.projects = append(c.projects, project)
	return project, nil
}

func saveProjects(path string, entries []projectEntry) error {
	raw, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	if len(raw)+1 > 64<<10 {
		return errors.New("project catalog exceeds 64 KiB")
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".projects-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	_, err = temp.Write(append(raw, '\n'))
	syncErr := temp.Sync()
	closeErr := temp.Close()
	if err := errors.Join(err, syncErr, closeErr); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}
