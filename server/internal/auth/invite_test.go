package auth_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"modupdater/internal/auth"
	"modupdater/internal/store"
)

func TestConcurrentRegisterOneInvite(t *testing.T) {
	db, au, _ := testStack(t, "adminpass1")
	admins, err := au.ListAdmins()
	if err != nil {
		t.Fatal(err)
	}
	tok, _, err := au.CreateInvite(admins[0].ID, 72)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	okN, invN := 0, 0
	var other []error
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			<-start
			_, err := au.Register(tok, fmt.Sprintf("user%dzz", n), "password12")
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				okN++
			} else if errors.Is(err, auth.ErrInviteInvalid) {
				invN++
			} else {
				other = append(other, err)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	if okN != 1 || invN != 1 || len(other) != 0 {
		t.Fatalf("ok %d invalid %d other %v", okN, invN, other)
	}
	list, err := au.ListAdmins()
	if err != nil {
		t.Fatal(err)
	}
	nonRoot := 0
	for _, a := range list {
		if !a.Root {
			nonRoot++
		}
	}
	if nonRoot != 1 {
		t.Fatal(nonRoot)
	}
	_ = db
}

func TestExpiredAndReusedInvite(t *testing.T) {
	db, au, _ := testStack(t, "adminpass1")
	expired := strings.Repeat("ab", 32)
	if err := db.InsertRawInvite(expired, "2000-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := au.Register(expired, "expireduser", "password12"); !errors.Is(err, auth.ErrInviteInvalid) {
		t.Fatal(err)
	}
	admins, err := au.ListAdmins()
	if err != nil {
		t.Fatal(err)
	}
	tok, _, err := au.CreateInvite(admins[0].ID, 72)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := au.Register(tok, "firstuser", "password12"); err != nil {
		t.Fatal(err)
	}
	if _, err := au.Register(tok, "secondusr", "password12"); !errors.Is(err, auth.ErrInviteInvalid) {
		t.Fatal(err)
	}
}

func TestDuplicateUsername(t *testing.T) {
	_, au, _ := testStack(t, "adminpass1")
	admins, err := au.ListAdmins()
	if err != nil {
		t.Fatal(err)
	}
	t1, _, err := au.CreateInvite(admins[0].ID, 72)
	if err != nil {
		t.Fatal(err)
	}
	t2, _, err := au.CreateInvite(admins[0].ID, 72)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := au.Register(t1, "sameuser", "password12"); err != nil {
		t.Fatal(err)
	}
	if _, err := au.Register(t2, "sameuser", "password12"); !errors.Is(err, auth.ErrUsernameTaken) {
		t.Fatal(err)
	}
	if _, err := au.Register(t2, "otheruser", "password12"); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteRootForbidden(t *testing.T) {
	_, _, h := testStack(t, "adminpass1")
	tok := loginToken(t, h)
	rec := authed(h, http.MethodGet, "/api/admin/admins", tok, "")
	var list struct {
		Admins []store.Admin `json:"admins"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(rec.Body.String())
	}
	var rootID string
	for _, a := range list.Admins {
		if a.Root {
			rootID = a.ID
		}
	}
	rec = authed(h, http.MethodDelete, "/api/admin/admins/"+rootID, tok, "")
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"error":"forbidden"`) {
		t.Fatal(rec.Code, rec.Body.String())
	}
	rec = authed(h, http.MethodGet, "/api/admin/admins", tok, "")
	if !strings.Contains(rec.Body.String(), rootID) {
		t.Fatal(rec.Body.String())
	}
}

func TestDeleteAdminDropsSessions(t *testing.T) {
	db, au, h := testStack(t, "adminpass1")
	root := loginToken(t, h)
	admins, err := au.ListAdmins()
	if err != nil {
		t.Fatal(err)
	}
	inv, _, err := au.CreateInvite(admins[0].ID, 72)
	if err != nil {
		t.Fatal(err)
	}
	res, err := au.Register(inv, "member1", "password12")
	if err != nil {
		t.Fatal(err)
	}
	n, err := db.SessionCount(res.Admin.ID)
	if err != nil || n != 1 {
		t.Fatalf("sessions %d %v", n, err)
	}
	rec := authed(h, http.MethodDelete, "/api/admin/admins/"+res.Admin.ID, root, "")
	if rec.Code != http.StatusNoContent {
		t.Fatal(rec.Code, rec.Body.String())
	}
	n, err = db.SessionCount(res.Admin.ID)
	if err != nil || n != 0 {
		t.Fatalf("sessions left %d %v", n, err)
	}
	me := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+res.Token)
	h.ServeHTTP(me, req)
	if me.Code != http.StatusUnauthorized {
		t.Fatal(me.Code, me.Body.String())
	}
}

func TestInviteHTMLDoesNotEchoFields(t *testing.T) {
	_, _, h := testStack(t, "adminpass1")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/invite/"+url.PathEscape("x</script>"), nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	if rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal(rec.Header().Get("Referrer-Policy"))
	}
	body := rec.Body.String()
	if strings.Contains(body, "</script>") || !strings.Contains(body, "&lt;/script&gt;") {
		t.Fatal(body)
	}
	if !strings.Contains(body, `action="/api/auth/register"`) || !strings.Contains(body, `name="invite_token"`) {
		t.Fatal(body)
	}
	user := "UniqueUserXYZ"
	pass := "SuperSecretPass99"
	form := url.Values{}
	form.Set("invite_token", "not-a-token")
	form.Set("username", user)
	form.Set("password", pass)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/auth/register", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "192.0.2.40:1"
	h.ServeHTTP(rec, req)
	got := rec.Body.String()
	if strings.Contains(got, user) || strings.Contains(got, pass) {
		t.Fatal(got)
	}
	if !strings.Contains(got, "Ссылка недействительна или имя занято.") {
		t.Fatal(got)
	}
}

func TestInviteTTLBounds(t *testing.T) {
	_, _, h := testStack(t, "adminpass1")
	tok := loginToken(t, h)
	for _, hours := range []int{0, 337} {
		rec := authed(h, http.MethodPost, "/api/admin/invites", tok, fmt.Sprintf(`{"ttl_hours":%d}`, hours))
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"error":"invalid_json"`) {
			t.Fatalf("%d: %d %s", hours, rec.Code, rec.Body.String())
		}
	}
	rec := authed(h, http.MethodPost, "/api/admin/invites", tok, `{"ttl_hours":336}`)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	var resp struct {
		Invite struct {
			Token string `json:"token"`
			URL   string `json:"url"`
		} `json:"invite"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(resp.Invite.URL)
	if err != nil {
		t.Fatal(err)
	}
	if u.Path != "/invite/"+resp.Invite.Token || u.Host != "127.0.0.1:8080" || u.RawQuery != "" {
		t.Fatal(resp.Invite.URL)
	}
}

func loginToken(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := do(h, http.MethodPost, "/api/auth/login", "192.0.2.50:1", `{"username":"admin","password":"adminpass1"}`)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Token == "" {
		t.Fatal(rec.Body.String())
	}
	return body.Token
}

func authed(h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "192.0.2.51:1"
	req.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	h.ServeHTTP(rec, req)
	return rec
}
