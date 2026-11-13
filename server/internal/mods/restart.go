package mods

import (
	"time"

	"modupdater/internal/store"
)

func (s *Service) SetServiceStart(fn func() (time.Time, bool)) {
	s.serviceStart = fn
}

func (s *Service) SetWorldHold(fn func(func() error) error) {
	s.holdWorld = fn
}

func (s *Service) RestartStatus() (string, []store.PendingRestart, error) {
	last, err := s.db.LastRestartAt()
	if err != nil {
		return "", nil, err
	}
	last, err = s.adoptServiceStart(last)
	if err != nil {
		return "", nil, err
	}
	pending, err := s.db.PendingRestart(last)
	if err != nil {
		return "", nil, err
	}
	return last, pending, nil
}

func (s *Service) noteServiceStart() error {
	last, err := s.db.LastRestartAt()
	if err != nil {
		return err
	}
	_, err = s.adoptServiceStart(last)
	return err
}

func (s *Service) adoptServiceStart(last string) (string, error) {
	if s.serviceStart == nil {
		return last, nil
	}
	at, ok := s.serviceStart()
	if !ok {
		return last, nil
	}
	at = at.UTC().Truncate(time.Second)
	if at.Unix() <= 0 {
		return last, nil
	}
	if last != "" {
		prev, err := time.Parse(time.RFC3339, last)
		if err == nil && !at.After(prev) {
			return last, nil
		}
	}
	ts := store.FormatTime(at)
	if err := s.db.SetLastRestartAt(ts); err != nil {
		return "", err
	}
	return ts, nil
}

func (s *Service) MarkRestart() (string, []store.PendingRestart, error) {
	now := store.Now()
	if err := s.db.SetLastRestartAt(now); err != nil {
		return "", nil, err
	}
	pending, err := s.db.PendingRestart(now)
	if err != nil {
		return "", nil, err
	}
	return now, pending, nil
}
