package mirror

import (
	"context"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sync/errgroup"
	"log/slog"
	"github.com/pkg/errors"
)

const (
	lockFilename = ".lock"
)

func updateMirrors(ctx context.Context, config *Config, mirrors []string) error {
	timestamp := time.Now()

	var mirrorList []*Mirror
	for _, mirrorID := range mirrors {
		mirror, err := NewMirror(timestamp, mirrorID, config)
		if err != nil {
			return err
		}
		mirrorList = append(mirrorList, mirror)
	}

	slog.Info("update starts")

	// run goroutines in an environment.
	group, ctx := errgroup.WithContext(ctx)

	for _, mirror := range mirrorList {
		mirror := mirror // capture loop variable
		group.Go(func() error {
			return mirror.Update(ctx)
		})
	}
	err := group.Wait()

	if err != nil {
		slog.Error("update failed", "error", err)
		return err
	}

	slog.Info("update ends")
	return nil
}

// gc removes old mirror files, if any.
func gc(ctx context.Context, config *Config) error {
	using := map[string]bool{
		lockFilename: true,
		".":          true,
		"..":         true,
	}

	dirEntries, err := ioutil.ReadDir(config.Dir)
	if err != nil {
		return err
	}

	// search symlinks and its pointing directories
	for _, dirEntry := range dirEntries {
		if (dirEntry.Mode() & os.ModeSymlink) == 0 {
			continue
		}
		filePath, err := filepath.EvalSymlinks(filepath.Join(config.Dir, dirEntry.Name()))
		if err != nil {
			return errors.Wrap(err, "gc")
		}
		using[dirEntry.Name()] = true
		using[filepath.Base(filepath.Dir(filePath))] = true
	}

	// remove unused dentries.
	for _, dirEntry := range dirEntries {
		if using[dirEntry.Name()] {
			continue
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		filePath := filepath.Join(config.Dir, dirEntry.Name())
		slog.Info("removing old mirror", "path", filePath)
		err := os.RemoveAll(filePath)
		if err != nil {
			return errors.Wrap(err, "gc")
		}
	}

	return nil
}

// Run starts mirroring.
//
// The first thing to do is to acquire flock on the lock file.
//
// mirrors is a list of mirror IDs defined in the configuration file
// (or keys in c.Mirrors).  If mirrors is an empty list, all mirrors
// will be updated.
func Run(config *Config, mirrors []string) error {
	lockFile := filepath.Join(config.Dir, lockFilename)
	file, err := os.Open(lockFile)
	switch {
	case os.IsNotExist(err):
		file2, err := os.OpenFile(lockFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			return err
		}
		file = file2
	case err != nil:
		return err
	}
	defer file.Close()

	fileLock := Flock{file}
	err = fileLock.Lock()
	if err != nil {
		return err
	}
	defer fileLock.Unlock()

	if len(mirrors) == 0 {
		for mirrorID := range config.Mirrors {
			mirrors = append(mirrors, mirrorID)
		}
	}

	group, ctx := errgroup.WithContext(context.Background())
	group.Go(func() error {
		err := updateMirrors(ctx, config, mirrors)
		if err != nil {
			if gcErr := gc(ctx, config); gcErr != nil {
				err = errors.Wrap(err, gcErr.Error())
			}
			return err
		}
		return gc(ctx, config)
	})
	return group.Wait()
}
