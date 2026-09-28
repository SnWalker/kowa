package migrations

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var filenamePattern = regexp.MustCompile(`^([0-9]{6})_([a-z0-9]+(?:_[a-z0-9]+)*)\.(up|down)\.sql$`)

type pair struct {
	name string
	up   bool
	down bool
}

// Check validates versioned, paired, non-empty SQL migration files.
func Check(directory string) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("read migrations %q: %w", directory, err)
	}

	pairs := map[string]pair{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		matches := filenamePattern.FindStringSubmatch(entry.Name())
		if matches == nil {
			return fmt.Errorf("invalid migration filename %q", entry.Name())
		}
		if err := checkNonEmpty(filepath.Join(directory, entry.Name())); err != nil {
			return err
		}

		version := matches[1]
		name := matches[2]
		direction := matches[3]
		migrationPair := pairs[version]
		if migrationPair.name != "" && migrationPair.name != name {
			return fmt.Errorf("migration version %s uses multiple names", version)
		}
		migrationPair.name = name
		switch direction {
		case "up":
			if migrationPair.up {
				return fmt.Errorf("migration version %s has duplicate up migration", version)
			}
			migrationPair.up = true
		case "down":
			if migrationPair.down {
				return fmt.Errorf("migration version %s has duplicate down migration", version)
			}
			migrationPair.down = true
		}
		pairs[version] = migrationPair
	}

	for version, migrationPair := range pairs {
		if !migrationPair.up {
			return fmt.Errorf("migration version %s is missing up migration", version)
		}
		if !migrationPair.down {
			return fmt.Errorf("migration version %s is missing down migration", version)
		}
	}

	return nil
}

func checkNonEmpty(path string) error {
	contents, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read migration %q: %w", path, err)
	}
	if strings.TrimSpace(string(contents)) == "" {
		return fmt.Errorf("migration %q is empty", path)
	}
	return nil
}
