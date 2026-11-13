package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"regexp"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	"modupdater/internal/store"
)

const bcryptCost = 12

var (
	ErrUnauthorized    = errors.New("unauthorized")
	ErrInvalidName     = errors.New("invalid_name")
	ErrInvalidPassword = errors.New("invalid_password")
	ErrInviteInvalid   = errors.New("invite_invalid")
	ErrUsernameTaken   = errors.New("username_taken")
	ErrForbidden       = errors.New("forbidden")
	ErrNotFound        = errors.New("not_found")
	ErrInvalidJSON     = errors.New("invalid_json")

	usernameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

	dummyOnce sync.Once
	dummyHash []byte
	dummyErr  error
)

type Result struct {
	Token        string
	RefreshToken string
	ExpiresAt    time.Time
	Admin        store.Admin
}

type Service struct {
	db    *store.DB
	dummy []byte
}

func New(db *store.DB) (*Service, error) {
	dummyOnce.Do(func() {
		buf := make([]byte, 32)
		if _, dummyErr = rand.Read(buf); dummyErr != nil {
			return
		}
		dummyHash, dummyErr = bcrypt.GenerateFromPassword(buf, bcryptCost)
	})
	if dummyErr != nil {
		return nil, dummyErr
	}
	cp := make([]byte, len(dummyHash))
	copy(cp, dummyHash)
	return &Service{db: db, dummy: cp}, nil
}

func ValidUsername(s string) bool {
	return len(s) >= 3 && len(s) <= 32 && usernameRe.MatchString(s)
}

func ValidPassword(s string) bool {
	return len(s) >= 8 && len(s) <= 128
}

func passwordMaterial(password string) []byte {
	b := []byte(password)
	if len(b) <= 72 {
		return b
	}
	sum := sha256.Sum256(b)
	return sum[:]
}

func (s *Service) BootstrapRoot(username, password string) (string, error) {
	ok, err := s.db.HasRoot()
	if err != nil {
		return "", err
	}
	if ok {
		return "", nil
	}
	generated := ""
	pw := password
	if pw == "" {
		generated, err = store.GeneratePassword(20)
		if err != nil {
			return "", err
		}
		pw = generated
	}
	hash, err := bcrypt.GenerateFromPassword(passwordMaterial(pw), bcryptCost)
	if err != nil {
		return "", err
	}
	if _, err := s.db.InsertAdmin(username, string(hash), true); err != nil {
		return "", err
	}
	return generated, nil
}

func (s *Service) Login(username, password string) (Result, error) {
	if !ValidUsername(username) {
		return Result{}, ErrInvalidName
	}
	if !ValidPassword(password) {
		return Result{}, ErrInvalidPassword
	}
	admin, hash, err := s.db.AdminByUsername(username)
	if errors.Is(err, store.ErrNotFound) {
		bcrypt.CompareHashAndPassword(s.dummy, passwordMaterial(password))
		return Result{}, ErrUnauthorized
	}
	if err != nil {
		return Result{}, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), passwordMaterial(password)) != nil {
		return Result{}, ErrUnauthorized
	}
	return s.issue(admin)
}

func (s *Service) issue(admin store.Admin) (Result, error) {
	tok, err := store.NewToken()
	if err != nil {
		return Result{}, err
	}
	ref, err := store.NewToken()
	if err != nil {
		return Result{}, err
	}
	exp := time.Now().UTC().Truncate(time.Second).Add(30 * 24 * time.Hour)
	expS := store.FormatTime(exp)
	if err := s.db.InsertSession(tok, admin.ID, expS); err != nil {
		return Result{}, err
	}
	if err := s.db.InsertRefresh(ref, admin.ID, expS); err != nil {
		_ = s.db.DeleteSession(tok)
		return Result{}, err
	}
	return Result{Token: tok, RefreshToken: ref, ExpiresAt: exp, Admin: admin}, nil
}

func (s *Service) Refresh(token string) (Result, error) {
	if !store.ValidToken(token) {
		return Result{}, ErrUnauthorized
	}
	admin, exp, err := s.db.ConsumeRefresh(token)
	if err != nil {
		return Result{}, ErrUnauthorized
	}
	t, err := time.Parse(time.RFC3339, exp)
	if err != nil || !time.Now().Before(t) {
		return Result{}, ErrUnauthorized
	}
	return s.issue(admin)
}

func (s *Service) Logout(token string) error {
	if !store.ValidToken(token) {
		return nil
	}
	return s.db.DeleteSession(token)
}

func (s *Service) AdminFromToken(token string) (store.Admin, error) {
	if !store.ValidToken(token) {
		return store.Admin{}, ErrUnauthorized
	}
	admin, exp, err := s.db.LookupSession(token)
	if err != nil {
		return store.Admin{}, ErrUnauthorized
	}
	t, err := time.Parse(time.RFC3339, exp)
	if err != nil || !time.Now().Before(t) {
		_ = s.db.DeleteSession(token)
		return store.Admin{}, ErrUnauthorized
	}
	return admin, nil
}

func (s *Service) Register(token, username, password string) (Result, error) {
	if !ValidUsername(username) {
		return Result{}, ErrInvalidName
	}
	if !ValidPassword(password) {
		return Result{}, ErrInvalidPassword
	}
	if !store.ValidToken(token) {
		return Result{}, ErrInviteInvalid
	}
	hash, err := bcrypt.GenerateFromPassword(passwordMaterial(password), bcryptCost)
	if err != nil {
		return Result{}, err
	}
	now := time.Now().UTC().Truncate(time.Second)
	admin, tok, refresh, exp, err := s.db.RegisterTx(username, string(hash), token, now)
	if errors.Is(err, store.ErrInviteInvalid) {
		return Result{}, ErrInviteInvalid
	}
	if errors.Is(err, store.ErrUsernameTaken) {
		return Result{}, ErrUsernameTaken
	}
	if err != nil {
		return Result{}, err
	}
	t, err := time.Parse(time.RFC3339, exp)
	if err != nil {
		return Result{}, err
	}
	return Result{Token: tok, RefreshToken: refresh, ExpiresAt: t, Admin: admin}, nil
}

func (s *Service) ListAdmins() ([]store.Admin, error) {
	return s.db.ListAdmins()
}

func (s *Service) DeleteAdmin(id string) error {
	if !store.ValidID(id) {
		return ErrNotFound
	}
	err := s.db.DeleteAdmin(id)
	if errors.Is(err, store.ErrForbidden) {
		return ErrForbidden
	}
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	}
	return err
}

func (s *Service) CreateInvite(adminID string, hours int) (string, string, error) {
	if hours < 1 || hours > 336 {
		return "", "", ErrInvalidJSON
	}
	if !store.ValidID(adminID) {
		return "", "", ErrNotFound
	}
	tok, err := store.NewToken()
	if err != nil {
		return "", "", err
	}
	exp := store.FormatTime(time.Now().UTC().Truncate(time.Second).Add(time.Duration(hours) * time.Hour))
	if err := s.db.InsertInvite(tok, adminID, exp); err != nil {
		return "", "", err
	}
	return tok, exp, nil
}

func (s *Service) DeleteInvite(token string) error {
	if !store.ValidToken(token) {
		return ErrNotFound
	}
	n, err := s.db.DeleteUnusedInvite(token)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
