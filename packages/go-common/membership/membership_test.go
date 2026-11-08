package membership

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fake(t *testing.T, role string, projectOK bool, orgStatus int) (*Client, func()) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/members/"):
			if orgStatus != 200 {
				w.WriteHeader(orgStatus)
				return
			}
			_, _ = w.Write([]byte(`{"role":"` + role + `"}`))
		case strings.Contains(r.URL.Path, "/projects/"):
			if !projectOK {
				w.WriteHeader(404)
			}
		}
	}))
	return &Client{OrgURL: srv.URL, ProjectURL: srv.URL, HTTP: http.DefaultClient}, srv.Close
}

func TestRequireRoles(t *testing.T) {
	for _, tc := range []struct {
		role string
		min  Role
		want int
	}{{"viewer", Viewer, 200}, {"viewer", Member, 403}, {"member", Member, 200}, {"member", Admin, 403}, {"admin", Admin, 200}, {"owner", Owner, 200}, {"admin", Owner, 403}} {
		c, done := fake(t, tc.role, true, 200)
		rec := httptest.NewRecorder()
		c.Require(rec, httptest.NewRequest("GET", "/", nil), "o", "u", tc.min)
		if rec.Code != tc.want {
			t.Errorf("%s needs %s: got %d want %d", tc.role, tc.min, rec.Code, tc.want)
		}
		done()
	}
}

func TestRequireFailsClosed(t *testing.T) {
	for _, status := range []int{404, 500} {
		c, done := fake(t, "owner", true, status)
		rec := httptest.NewRecorder()
		_, ok := c.Require(rec, httptest.NewRequest("GET", "/", nil), "o", "u", Viewer)
		want := map[int]int{404: 403, 500: 502}[status]
		if ok || rec.Code != want {
			t.Errorf("org status %d => ok=%v code=%d want %d", status, ok, rec.Code, want)
		}
		done()
	}
	c := &Client{} // not configured
	rec := httptest.NewRecorder()
	if _, ok := c.Require(rec, httptest.NewRequest("GET", "/", nil), "o", "u", Viewer); ok || rec.Code != 502 {
		t.Errorf("unconfigured client must fail closed, got ok=%v %d", ok, rec.Code)
	}
}

func TestRequireProjectBinding(t *testing.T) {
	c, done := fake(t, "owner", false, 200)
	defer done()
	rec := httptest.NewRecorder()
	if _, ok := c.RequireProject(rec, httptest.NewRequest("GET", "/", nil), "o", "p", "u", Viewer); ok || rec.Code != 404 {
		t.Fatalf("project outside org must be 404, got ok=%v %d", ok, rec.Code)
	}
}
