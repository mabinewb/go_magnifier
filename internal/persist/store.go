package persist

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gomagnifier/internal/model"
)

type Store struct {
	rootDir     string
	profilesDir string
	statePath   string
	sessionPath string
	settingsPath string
}

func (s *Store) RootDir() string {
	if s == nil {
		return ""
	}
	return s.rootDir
}

func (s *Store) SettingsPath() string {
	if s == nil {
		return ""
	}
	return s.settingsPath
}

func NewStore(appName string) (*Store, error) {
	baseDir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve config directory: %w", err)
	}

	rootDir := filepath.Join(baseDir, appName)
	profilesDir := filepath.Join(rootDir, "profiles")
	store := &Store{
		rootDir:     rootDir,
		profilesDir: profilesDir,
		statePath:   filepath.Join(rootDir, "state.json"),
		sessionPath: filepath.Join(rootDir, "session.json"),
		settingsPath: filepath.Join(rootDir, "settings.json"),
	}

	if err := os.MkdirAll(profilesDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create config directory: %w", err)
	}

	return store, nil
}

func (s *Store) SaveProfile(name string, profile model.Profile) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("preset name is required")
	}

	profile.Name = name
	return s.writeJSON(filepath.Join(s.profilesDir, sanitizeFileName(name)+".json"), profile)
}

func (s *Store) LoadProfile(name string) (model.Profile, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return model.Profile{}, errors.New("preset name is required")
	}

	var profile model.Profile
	err := s.readJSON(filepath.Join(s.profilesDir, sanitizeFileName(name)+".json"), &profile)
	if err != nil {
		return model.Profile{}, err
	}
	return profile.Sanitized(), nil
}

func (s *Store) DeleteProfile(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("preset name is required")
	}
	err := os.Remove(filepath.Join(s.profilesDir, sanitizeFileName(name)+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *Store) ListProfiles() ([]string, error) {
	entries, err := os.ReadDir(s.profilesDir)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		filePath := filepath.Join(s.profilesDir, entry.Name())
		var profile model.Profile
		if err := s.readJSON(filePath, &profile); err == nil && strings.TrimSpace(profile.Name) != "" {
			names = append(names, profile.Name)
			continue
		}

		names = append(names, strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())))
	}

	sort.Strings(names)
	return names, nil
}

func (s *Store) SaveState(state model.State) error {
	return s.writeJSON(s.statePath, state)
}

func (s *Store) LoadState() (model.State, error) {
	var state model.State
	err := s.readJSON(s.statePath, &state)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	return state, err
}

func (s *Store) SaveSession(session model.Session) error {
	return s.writeJSON(s.sessionPath, session)
}

func (s *Store) LoadSession() (model.Session, error) {
	var session model.Session
	err := s.readJSON(s.sessionPath, &session)
	if errors.Is(err, os.ErrNotExist) {
		return session, nil
	}
	if err == nil {
		if len(session.Overlays) == 0 {
			return session, nil
		}
		for index := range session.Overlays {
			session.Overlays[index] = session.Overlays[index].Sanitized()
		}
		return session, nil
	}

	var legacyProfile model.Profile
	legacyErr := s.readJSON(s.sessionPath, &legacyProfile)
	if legacyErr == nil {
		legacyProfile = legacyProfile.Sanitized()
		return model.Session{Overlays: []model.Profile{legacyProfile}}, nil
	}
	return model.Session{}, err
}

func (s *Store) readJSON(path string, target any) error {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(bytes, target)
}

func (s *Store) writeJSON(path string, value any) error {
	bytes, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, bytes, 0o644)
}

func sanitizeFileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "preset"
	}

	var builder strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			builder.WriteRune(r)
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '-' || r == '_':
			builder.WriteRune(r)
		default:
			builder.WriteRune('_')
		}
	}

	clean := strings.Trim(builder.String(), "_")
	if clean == "" {
		return "preset"
	}
	return clean
}

func (s *Store) SaveSettings(session model.Session) error {
	return s.writeJSON(s.settingsPath, session)
}

func (s *Store) LoadSettings() (model.Session, error) {
	return s.LoadSettingsFrom(s.settingsPath)
}

func (s *Store) SaveSettingsAs(path string, session model.Session) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return s.writeJSON(path, session)
}

func (s *Store) LoadSettingsFrom(path string) (model.Session, error) {
	var session model.Session
	err := s.readJSON(path, &session)
	if errors.Is(err, os.ErrNotExist) {
		return session, nil
	}
	if err == nil {
		for index := range session.Overlays {
			session.Overlays[index] = session.Overlays[index].Sanitized()
		}
		return session, nil
	}

	var legacyProfile model.Profile
	legacyErr := s.readJSON(path, &legacyProfile)
	if legacyErr == nil {
		legacyProfile = legacyProfile.Sanitized()
		return model.Session{Overlays: []model.Profile{legacyProfile}}, nil
	}
	return model.Session{}, err
}