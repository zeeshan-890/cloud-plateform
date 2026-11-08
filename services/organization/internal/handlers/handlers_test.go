package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jp-cloud/go-common/internalauth"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/organization/internal/store"
)

const testSecret = "0123456789abcdef0123456789abcdef0123"

// ---------------------------------------------------------------- fake repo

type fakeRepo struct {
	mu      sync.Mutex
	orgs    map[string]*store.Org
	members map[string]map[string]string // org -> user -> role
	invites map[string]*fakeInvite
	grants  []store.Grant
	managed map[string]bool
}

type fakeInvite struct {
	store.Invite
	hash                     string
	accepted, revoked        bool
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{orgs: map[string]*store.Org{}, members: map[string]map[string]string{}, invites: map[string]*fakeInvite{}, managed: map[string]bool{}}
}

func (f *fakeRepo) CreateOrg(_ context.Context, name, slug, owner string) (*store.Org, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, o := range f.orgs {
		if o.Slug == slug {
			return nil, store.ErrAlreadyExists
		}
	}
	o := &store.Org{ID: uuid.NewString(), Name: name, Slug: slug, Role: "owner", CreatedAt: time.Now()}
	f.orgs[o.ID] = o
	f.members[o.ID] = map[string]string{owner: "owner"}
	return o, nil
}
func (f *fakeRepo) ListOrgsForUser(_ context.Context, uid string) ([]store.Org, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []store.Org{}
	for id, m := range f.members {
		if r, ok := m[uid]; ok {
			o := *f.orgs[id]
			o.Role = r
			out = append(out, o)
		}
	}
	return out, nil
}
func (f *fakeRepo) GetOrg(_ context.Context, orgID, uid string) (*store.Org, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.members[orgID][uid]
	if !ok {
		return nil, store.ErrNotFound
	}
	o := *f.orgs[orgID]
	o.Role = r
	return &o, nil
}
func (f *fakeRepo) GetOrgByID(_ context.Context, orgID string) (*store.Org, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.orgs[orgID]
	if !ok {
		return nil, store.ErrNotFound
	}
	c := *o
	return &c, nil
}
func (f *fakeRepo) MemberRole(_ context.Context, orgID, uid string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.members[orgID][uid]
	if !ok {
		return "", store.ErrNotFound
	}
	return r, nil
}
func (f *fakeRepo) DeleteOrg(_ context.Context, orgID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.orgs[orgID]; !ok {
		return store.ErrNotFound
	}
	delete(f.orgs, orgID)
	delete(f.members, orgID)
	return nil
}
func (f *fakeRepo) CreateInvite(_ context.Context, orgID, email, role, hash, by string, exp time.Time) (*store.Invite, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fi := &fakeInvite{Invite: store.Invite{ID: uuid.NewString(), OrgID: orgID, Email: email, Role: role, InvitedBy: by, ExpiresAt: exp}, hash: hash}
	f.invites[fi.ID] = fi
	inv := fi.Invite
	return &inv, nil
}
func (f *fakeRepo) CountPendingInvites(_ context.Context, orgID string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, i := range f.invites {
		if i.OrgID == orgID && !i.accepted && !i.revoked {
			n++
		}
	}
	return n, nil
}
func (f *fakeRepo) ListInvites(_ context.Context, orgID string) ([]store.Invite, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []store.Invite{}
	for _, i := range f.invites {
		if i.OrgID == orgID && !i.accepted && !i.revoked {
			out = append(out, i.Invite)
		}
	}
	return out, nil
}
func (f *fakeRepo) RevokeInvite(_ context.Context, orgID, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	i, ok := f.invites[id]
	if !ok || i.OrgID != orgID || i.accepted || i.revoked {
		return store.ErrNotFound
	}
	i.revoked = true
	return nil
}
func (f *fakeRepo) CheckInvite(_ context.Context, hash, email string) (*store.Invite, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, i := range f.invites {
		if i.hash == hash && !i.accepted && !i.revoked && time.Now().Before(i.ExpiresAt) && strings.EqualFold(i.Email, email) {
			inv := i.Invite
			return &inv, nil
		}
	}
	return nil, store.ErrNotFound
}
func (f *fakeRepo) AcceptInvite(_ context.Context, hash, uid, email string) (*store.Org, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, i := range f.invites {
		if i.hash != hash {
			continue
		}
		if i.revoked || time.Now().After(i.ExpiresAt) {
			return nil, "", store.ErrNotFound
		}
		if i.accepted {
			return nil, "", store.ErrAlreadyExists
		}
		if !strings.EqualFold(i.Email, email) {
			return nil, "", store.ErrForbidden
		}
		i.accepted = true
		if _, exists := f.members[i.OrgID][uid]; !exists {
			f.members[i.OrgID][uid] = i.Role
		}
		o := *f.orgs[i.OrgID]
		o.Role = f.members[i.OrgID][uid]
		return &o, o.Role, nil
	}
	return nil, "", store.ErrNotFound
}
func (f *fakeRepo) ListMembers(_ context.Context, orgID string) ([]store.Member, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []store.Member{}
	for u, r := range f.members[orgID] {
		out = append(out, store.Member{UserID: u, Role: r, Managed: f.managed[orgID+"|"+u]})
	}
	return out, nil
}
func (f *fakeRepo) CountMembers(_ context.Context, orgID string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.members[orgID]), nil
}
func (f *fakeRepo) owners(orgID string) int {
	n := 0
	for _, r := range f.members[orgID] {
		if r == "owner" {
			n++
		}
	}
	return n
}
func (f *fakeRepo) SetMemberRole(_ context.Context, orgID, uid, role string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur, ok := f.members[orgID][uid]
	if !ok {
		return store.ErrNotFound
	}
	if cur == "owner" && role != "owner" && f.owners(orgID) == 1 {
		return store.ErrLastOwner
	}
	f.members[orgID][uid] = role
	return nil
}
func (f *fakeRepo) RemoveMember(_ context.Context, orgID, uid string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur, ok := f.members[orgID][uid]
	if !ok {
		return store.ErrNotFound
	}
	if cur == "owner" && f.owners(orgID) == 1 {
		return store.ErrLastOwner
	}
	delete(f.members[orgID], uid)
	keep := f.grants[:0]
	for _, g := range f.grants {
		if !(g.OrgID == orgID && g.UserID == uid) {
			keep = append(keep, g)
		}
	}
	f.grants = keep
	return nil
}

// ---------------------------------------------------------------- harness

type harness struct {
	t      *testing.T
	h      *Handler
	mux    *http.ServeMux
	repo   *fakeRepo
	jm     *jwtutil.Manager
	notify []map[string]any
	nmu    sync.Mutex
	delete map[string]int // project id -> status to return
	projects map[string]string // existing project id -> name
	identity *fakeIdentity
	aud      *capAudit
	dmu    sync.Mutex
	deleted []string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	hs := &harness{t: t, repo: newFakeRepo(), jm: jwtutil.NewManager(testSecret, time.Minute, time.Hour), delete: map[string]int{}, projects: map[string]string{}}

	notif := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		hs.nmu.Lock()
		hs.notify = append(hs.notify, m)
		hs.nmu.Unlock()
		w.WriteHeader(202)
	}))
	t.Cleanup(notif.Close)

	proj := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		switch {
		case r.Method == "GET" && len(parts) == 4 && parts[3] == "projects":
			hs.dmu.Lock()
			defer hs.dmu.Unlock()
			var ids []map[string]string
			for id := range hs.delete {
				ids = append(ids, map[string]string{"id": id})
			}
			for id, name := range hs.projects {
				ids = append(ids, map[string]string{"id": id, "name": name})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"projects": ids})
		case r.Method == "GET" && len(parts) == 5:
			hs.dmu.Lock()
			defer hs.dmu.Unlock()
			if _, ok := hs.projects[parts[4]]; ok {
				w.WriteHeader(200)
				return
			}
			w.WriteHeader(404)
		case r.Method == "DELETE" && len(parts) == 5:
			hs.dmu.Lock()
			defer hs.dmu.Unlock()
			hs.deleted = append(hs.deleted, parts[4])
			st := hs.delete[parts[4]]
			if st == 0 {
				st = 204
			}
			w.WriteHeader(st)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(proj.Close)

	hs.identity = newFakeIdentity()
	idsrv := httptest.NewServer(hs.identity)
	t.Cleanup(idsrv.Close)
	hs.aud = &capAudit{}
	hs.h = &Handler{Store: hs.repo, JWT: hs.jm, NotificationURL: notif.URL, ProjectURL: proj.URL, IdentityURL: idsrv.URL, HTTP: http.DefaultClient, Audit: hs.aud}
	hs.mux = http.NewServeMux()
	hs.h.Routes(hs.mux)
	return hs
}

type user struct{ id, email, token string }

func (hs *harness) user(email string) user {
	id := uuid.NewString()
	tok, _, _ := hs.jm.IssueAccess(id, email, email)
	return user{id: id, email: email, token: tok}
}

func (hs *harness) do(method, path string, body any, token string, hdr ...string) *httptest.ResponseRecorder {
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	hs.mux.ServeHTTP(rec, req)
	return rec
}

func body(rec *httptest.ResponseRecorder) map[string]any {
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	return m
}

// orgWith creates an org owned by owner and adds the given members (email -> role).
func (hs *harness) orgWith(owner user, others map[user]string) string {
	rec := hs.do("POST", "/orgs", map[string]string{"name": "Acme", "slug": "acme-" + uuid.NewString()[:6]}, owner.token)
	if rec.Code != 201 {
		hs.t.Fatalf("create org: %d %s", rec.Code, rec.Body)
	}
	id := body(rec)["org"].(map[string]any)["id"].(string)
	for u, role := range others {
		hs.repo.members[id][u.id] = role
	}
	return id
}

// ---------------------------------------------------------------- tests

func TestSlugValidation(t *testing.T) {
	hs := newHarness(t)
	u := hs.user("o@example.com")
	cases := map[string]int{
		"acme": 201, "a1": 201, "my-org-2": 201,
		"a": 400, "-bad": 400, "Bad_Slug": 400, "with space": 400, "has.dot": 400,
		strings.Repeat("a", 40): 400, "": 400, "../etc": 400,
	}
	for slug, want := range cases {
		rec := hs.do("POST", "/orgs", map[string]string{"name": "X", "slug": slug}, u.token)
		if slug == "Bad_Slug" { // lower-cased first, still invalid because of '_'
			want = 400
		}
		if rec.Code != want {
			t.Errorf("slug %q: got %d want %d (%s)", slug, rec.Code, want, rec.Body)
		}
	}
	if rec := hs.do("POST", "/orgs", map[string]string{"name": "X", "slug": "acme"}, u.token); rec.Code != 409 {
		t.Errorf("duplicate slug should be 409, got %d", rec.Code)
	}
}

func TestOnlyOwnerOrAdminCanInvite(t *testing.T) {
	hs := newHarness(t)
	owner, admin, member, viewer, outsider := hs.user("o@e.com"), hs.user("a@e.com"), hs.user("m@e.com"), hs.user("v@e.com"), hs.user("x@e.com")
	org := hs.orgWith(owner, map[user]string{admin: "admin", member: "member", viewer: "viewer"})
	invite := func(u user, role string) int {
		return hs.do("POST", "/orgs/"+org+"/invites", map[string]string{"email": "new@example.com", "role": role}, u.token).Code
	}
	if c := invite(viewer, "viewer"); c != 403 {
		t.Errorf("viewer invite = %d", c)
	}
	if c := invite(member, "viewer"); c != 403 {
		t.Errorf("member invite = %d", c)
	}
	if c := invite(outsider, "viewer"); c != 404 {
		t.Errorf("non-member invite = %d, want 404", c)
	}
	if c := invite(admin, "owner"); c != 403 {
		t.Errorf("admin must not mint owner invites, got %d", c)
	}
	if c := invite(admin, "admin"); c != 403 {
		t.Errorf("admin must not mint admin invites, got %d", c)
	}
	if c := invite(admin, "member"); c != 201 {
		t.Errorf("admin invite = %d", c)
	}
	if c := invite(owner, "owner"); c != 201 {
		t.Errorf("owner may invite owner, got %d", c)
	}
}

func TestInviteTokenHashedAndEmailed(t *testing.T) {
	hs := newHarness(t)
	owner := hs.user("o@e.com")
	org := hs.orgWith(owner, nil)
	rec := hs.do("POST", "/orgs/"+org+"/invites", map[string]string{"email": "Invitee@Example.com", "role": "member"}, owner.token)
	if rec.Code != 201 {
		t.Fatalf("invite: %d %s", rec.Code, rec.Body)
	}
	out := body(rec)
	token := out["token"].(string)
	if len(token) < 48 {
		t.Fatalf("token too short: %q", token)
	}
	if out["email"] != "invitee@example.com" || out["email_delivery"] != "queued" {
		t.Fatalf("response: %v", out)
	}
	for _, inv := range hs.repo.invites {
		if inv.hash == token || strings.Contains(inv.hash, token) || inv.hash != store.HashToken(token) {
			t.Fatal("only the hash may be stored")
		}
	}
	hs.nmu.Lock()
	defer hs.nmu.Unlock()
	if len(hs.notify) != 1 || hs.notify[0]["type"] != "org.invite" || hs.notify[0]["token"] != token || hs.notify[0]["email"] != "invitee@example.com" {
		t.Fatalf("notification not triggered correctly: %v", hs.notify)
	}
	// listing never exposes the token
	list := hs.do("GET", "/orgs/"+org+"/invites", nil, owner.token)
	if list.Code != 200 || strings.Contains(list.Body.String(), token) {
		t.Fatalf("list leaked token or failed: %d", list.Code)
	}
}

func TestAcceptInviteBoundToEmailSingleUse(t *testing.T) {
	hs := newHarness(t)
	owner := hs.user("o@e.com")
	org := hs.orgWith(owner, nil)
	token := body(hs.do("POST", "/orgs/"+org+"/invites", map[string]string{"email": "invitee@example.com", "role": "member"}, owner.token))["token"].(string)

	attacker := hs.user("attacker@example.com")
	if rec := hs.do("POST", "/orgs/invites/accept", map[string]string{"token": token}, attacker.token); rec.Code != 404 {
		t.Fatalf("invite for another email must be rejected (404), got %d", rec.Code)
	}
	if _, ok := hs.repo.members[org][attacker.id]; ok {
		t.Fatal("attacker joined the org")
	}
	invitee := hs.user("invitee@example.com")
	if rec := hs.do("POST", "/orgs/invites/accept", map[string]string{"token": "deadbeef"}, invitee.token); rec.Code != 404 {
		t.Fatalf("garbage token: %d", rec.Code)
	}
	if rec := hs.do("POST", "/orgs/invites/accept", map[string]string{"token": token}, invitee.token); rec.Code != 200 {
		t.Fatalf("legit accept: %d %s", rec.Code, rec.Body)
	}
	if hs.repo.members[org][invitee.id] != "member" {
		t.Fatal("invitee should be a member")
	}
	if rec := hs.do("POST", "/orgs/invites/accept", map[string]string{"token": token}, invitee.token); rec.Code != 409 {
		t.Fatalf("second accept must fail (single-use), got %d", rec.Code)
	}
}

func TestAcceptInviteConcurrentSingleUse(t *testing.T) {
	hs := newHarness(t)
	owner := hs.user("o@e.com")
	org := hs.orgWith(owner, nil)
	token := body(hs.do("POST", "/orgs/"+org+"/invites", map[string]string{"email": "invitee@example.com", "role": "member"}, owner.token))["token"].(string)
	invitee := hs.user("invitee@example.com")
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if hs.do("POST", "/orgs/invites/accept", map[string]string{"token": token}, invitee.token).Code == 200 {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if ok != 1 {
		t.Fatalf("exactly one concurrent accept may win, got %d", ok)
	}
}

func TestInviteRevokeAndExpiry(t *testing.T) {
	hs := newHarness(t)
	owner, member := hs.user("o@e.com"), hs.user("m@e.com")
	org := hs.orgWith(owner, map[user]string{member: "member"})
	out := body(hs.do("POST", "/orgs/"+org+"/invites", map[string]string{"email": "invitee@example.com", "role": "viewer"}, owner.token))
	if rec := hs.do("DELETE", "/orgs/"+org+"/invites/"+out["id"].(string), nil, member.token); rec.Code != 403 {
		t.Fatalf("member must not revoke invites, got %d", rec.Code)
	}
	if rec := hs.do("GET", "/orgs/"+org+"/invites", nil, member.token); rec.Code != 403 {
		t.Fatalf("member must not list invites, got %d", rec.Code)
	}
	if rec := hs.do("DELETE", "/orgs/"+org+"/invites/"+out["id"].(string), nil, owner.token); rec.Code != 204 {
		t.Fatalf("revoke: %d", rec.Code)
	}
	invitee := hs.user("invitee@example.com")
	if rec := hs.do("POST", "/orgs/invites/accept", map[string]string{"token": out["token"].(string)}, invitee.token); rec.Code != 404 {
		t.Fatalf("revoked invite must not be accepted, got %d", rec.Code)
	}
	// expiry
	out2 := body(hs.do("POST", "/orgs/"+org+"/invites", map[string]string{"email": "invitee@example.com", "role": "viewer"}, owner.token))
	for _, i := range hs.repo.invites {
		i.ExpiresAt = time.Now().Add(-time.Minute)
	}
	if rec := hs.do("POST", "/orgs/invites/accept", map[string]string{"token": out2["token"].(string)}, invitee.token); rec.Code != 404 {
		t.Fatalf("expired invite must not be accepted, got %d", rec.Code)
	}
}

func TestAcceptNeverDowngradesExistingMember(t *testing.T) {
	hs := newHarness(t)
	owner := hs.user("o@e.com")
	org := hs.orgWith(owner, nil)
	token := body(hs.do("POST", "/orgs/"+org+"/invites", map[string]string{"email": "o@e.com", "role": "viewer"}, owner.token))["token"].(string)
	hs.do("POST", "/orgs/invites/accept", map[string]string{"token": token}, owner.token)
	if hs.repo.members[org][owner.id] != "owner" {
		t.Fatal("owner was downgraded by accepting a viewer invite")
	}
}

func TestMemberRoleChangeAndRemovalRules(t *testing.T) {
	hs := newHarness(t)
	owner, admin, member, other := hs.user("o@e.com"), hs.user("a@e.com"), hs.user("m@e.com"), hs.user("m2@e.com")
	org := hs.orgWith(owner, map[user]string{admin: "admin", member: "member", other: "member"})
	patch := func(actor user, target user, role string) int {
		return hs.do("PATCH", "/orgs/"+org+"/members/"+target.id, map[string]string{"role": role}, actor.token).Code
	}
	if c := patch(admin, member, "admin"); c != 403 {
		t.Errorf("admin must not change roles, got %d", c)
	}
	if c := patch(member, member, "owner"); c != 403 {
		t.Errorf("member must not self-promote, got %d", c)
	}
	if c := patch(owner, member, "superuser"); c != 400 {
		t.Errorf("invalid role: %d", c)
	}
	if c := patch(owner, member, "admin"); c != 200 || hs.repo.members[org][member.id] != "admin" {
		t.Errorf("owner role change: %d", c)
	}
	if c := patch(owner, owner, "member"); c != 409 {
		t.Errorf("last owner must not be demotable, got %d", c)
	}
	// removal
	del := func(actor user, target user) int {
		return hs.do("DELETE", "/orgs/"+org+"/members/"+target.id, nil, actor.token).Code
	}
	if c := del(other, member); c != 403 {
		t.Errorf("member removing someone: %d", c)
	}
	if c := del(admin, member); c != 403 { // member is now an admin
		t.Errorf("admin must not remove another admin: %d", c)
	}
	if c := del(owner, owner); c != 409 {
		t.Errorf("last owner must not be removable, got %d", c)
	}
	if c := del(other, other); c != 204 {
		t.Errorf("a member may leave the org, got %d", c)
	}
	if c := del(admin, member); c != 403 {
		t.Errorf("still forbidden: %d", c)
	}
	hs.repo.members[org][member.id] = "viewer"
	if c := del(admin, member); c != 204 {
		t.Errorf("admin may remove a viewer: %d", c)
	}
	// with two owners one may leave
	hs.repo.members[org][admin.id] = "owner"
	if c := del(owner, owner); c != 204 {
		t.Errorf("owner may leave when another owner exists: %d", c)
	}
}

func TestDeleteOrgOwnerOnlyWithCascade(t *testing.T) {
	hs := newHarness(t)
	owner, admin, outsider := hs.user("o@e.com"), hs.user("a@e.com"), hs.user("x@e.com")
	org := hs.orgWith(owner, map[user]string{admin: "admin"})
	hs.delete["p1"], hs.delete["p2"] = 204, 204

	if rec := hs.do("DELETE", "/orgs/"+org, nil, admin.token); rec.Code != 403 {
		t.Fatalf("admin must not delete the org: %d", rec.Code)
	}
	if rec := hs.do("DELETE", "/orgs/"+org, nil, outsider.token); rec.Code != 404 {
		t.Fatalf("outsider: %d", rec.Code)
	}
	if len(hs.deleted) != 0 {
		t.Fatal("no cascade may run for unauthorised callers")
	}

	// one project fails -> org kept, failure reported
	hs.delete["p2"] = 500
	rec := hs.do("DELETE", "/orgs/"+org, nil, owner.token)
	if rec.Code != 502 || !strings.Contains(rec.Body.String(), "p2") || !strings.Contains(rec.Body.String(), "partial_failure") {
		t.Fatalf("partial failure must be surfaced: %d %s", rec.Code, rec.Body)
	}
	if _, err := hs.repo.GetOrgByID(context.Background(), org); err != nil {
		t.Fatal("org must survive a failed cascade")
	}

	hs.delete["p2"] = 204
	hs.deleted = nil
	if rec := hs.do("DELETE", "/orgs/"+org, nil, owner.token); rec.Code != 204 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if len(hs.deleted) != 2 {
		t.Fatalf("every project must be deleted via the project service, got %v", hs.deleted)
	}
	if _, err := hs.repo.GetOrgByID(context.Background(), org); err == nil {
		t.Fatal("org should be gone")
	}
}

func TestInternalEndpointsRequireToken(t *testing.T) {
	t.Setenv("INTERNAL_TOKEN", "internal-token-for-tests")
	hs := newHarness(t)
	owner := hs.user("o@e.com")
	org := hs.orgWith(owner, nil)
	for _, p := range []struct{ m, path string }{
		{"GET", "/internal/orgs/" + org}, {"GET", "/internal/orgs/" + org + "/members/" + owner.id}, {"POST", "/internal/invites/check"},
	} {
		if rec := hs.do(p.m, p.path, map[string]string{}, owner.token); rec.Code != 403 {
			t.Errorf("%s %s should be 403 without the internal token (user JWT is not enough), got %d", p.m, p.path, rec.Code)
		}
	}
	rec := hs.do("GET", "/internal/orgs/"+org+"/members/"+owner.id, nil, "", internalauth.Header, "internal-token-for-tests")
	if rec.Code != 200 || body(rec)["role"] != "owner" {
		t.Fatalf("internal member lookup: %d %s", rec.Code, rec.Body)
	}
	rec = hs.do("GET", "/internal/orgs/"+org, nil, "", internalauth.Header, "internal-token-for-tests")
	if rec.Code != 200 || body(rec)["slug"] == nil {
		t.Fatalf("internal org lookup: %d %s", rec.Code, rec.Body)
	}
	if rec := hs.do("GET", "/internal/orgs/"+uuid.NewString(), nil, "", internalauth.Header, "internal-token-for-tests"); rec.Code != 404 {
		t.Fatalf("unknown org should be 404: %d", rec.Code)
	}
	// invite check is email-bound
	token := body(hs.do("POST", "/orgs/"+org+"/invites", map[string]string{"email": "i@example.com", "role": "viewer"}, owner.token))["token"].(string)
	chk := func(tok, email string) int {
		return hs.do("POST", "/internal/invites/check", map[string]string{"token": tok, "email": email}, "", internalauth.Header, "internal-token-for-tests").Code
	}
	if chk(token, "i@example.com") != 200 || chk(token, "other@example.com") != 404 || chk("nope", "i@example.com") != 404 {
		t.Fatal("invite check must bind token to email")
	}
}

func TestViewerCanReadMembersButNotMutate(t *testing.T) {
	hs := newHarness(t)
	owner, viewer := hs.user("o@e.com"), hs.user("v@e.com")
	org := hs.orgWith(owner, map[user]string{viewer: "viewer"})
	if rec := hs.do("GET", "/orgs/"+org+"/members", nil, viewer.token); rec.Code != 200 {
		t.Fatalf("viewer list members: %d", rec.Code)
	}
	if rec := hs.do("DELETE", "/orgs/"+org+"/members/"+owner.id, nil, viewer.token); rec.Code != 403 {
		t.Fatalf("viewer removing owner: %d", rec.Code)
	}
}
