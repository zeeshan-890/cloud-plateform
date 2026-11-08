package e2e

import (
	"bytes"
	"encoding/base64"
	"net/url"
	"strings"
	"testing"
)

func TestStorage_BucketObjectsAndIsolation(t *testing.T) {
	admin := AdminClient(t)
	org := NewOrg(t, admin)
	pa := NewProject(t, admin, org)
	pb := NewProject(t, admin, org)

	if r := admin.Do("GET", pa.Path("/storage/bucket"), nil); r.Status != 404 {
		t.Errorf("bucket before creation: want 404, got %d", r.Status)
	}
	br := admin.Must(t, "POST", pa.Path("/storage/bucket"), nil, 200).Map(t)
	if Str(br, "bucket", "name") == "" {
		t.Fatalf("bucket response: %v", br)
	}
	admin.Must(t, "GET", pa.Path("/storage/bucket"), nil, 200)
	// bucket names are per project and never contain user supplied text
	pbr := admin.Must(t, "POST", pb.Path("/storage/bucket"), nil, 200).Map(t)
	if Str(pbr, "bucket", "name") == Str(br, "bucket", "name") {
		t.Errorf("two projects share a bucket: %s", Str(br, "bucket", "name"))
	}

	// ---- base64 upload
	payload := []byte("hello e2e storage \x00\x01\x02 binary tail")
	up := admin.Must(t, "POST", pa.Path("/storage/objects"), map[string]string{
		"key": "docs/a.bin", "content_base64": base64.StdEncoding.EncodeToString(payload), "content_type": "application/octet-stream"}, 201).Map(t)
	if Num(up, "object", "size_bytes") != float64(len(payload)) {
		t.Errorf("size_bytes = %v, want %d", Dig(up, "object", "size_bytes"), len(payload))
	}
	if r := admin.Do("POST", pa.Path("/storage/objects"), map[string]string{"key": "docs/bad.txt", "content_base64": "!!!not base64!!!"}); r.Status != 400 {
		t.Errorf("invalid base64: want 400, got %d", r.Status)
	}

	// ---- streaming PUT (large-ish, binary)
	stream := bytes.Repeat([]byte{0xAB, 0xCD, 0xEF, 0x01}, 512*1024) // 2 MiB
	pr := admin.DoRaw("PUT", pa.Path("/storage/objects?key="+url.QueryEscape("big/stream.bin")), stream, "application/octet-stream", nil)
	streamOK := pr.Status == 201
	if !streamOK {
		t.Errorf("GAP/FAIL: streaming PUT /storage/objects?key=... -> %d %s (the gateway route table must expose it)", pr.Status, truncate(string(pr.Body), 200))
	}

	// ---- list with prefix and usage
	lr := admin.Must(t, "GET", pa.Path("/storage/objects?prefix=docs/"), nil, 200).Map(t)
	objs := Arr(lr, "objects")
	if len(objs) != 1 || Str(objs[0], "key") != "docs/a.bin" {
		t.Errorf("prefix list = %v", objs)
	}
	if Num(lr, "usage", "used_bytes") < float64(len(payload)) {
		t.Errorf("usage not accounted: %v", Dig(lr, "usage"))
	}
	if r := admin.Do("GET", pa.Path("/storage/objects?limit=0"), nil); r.Status != 400 {
		t.Errorf("limit=0: want 400, got %d", r.Status)
	}

	// ---- download through the API (via signed-url path and directly)
	su := admin.Must(t, "POST", pa.Path("/storage/signed-url"), map[string]any{"key": "docs/a.bin", "expires_in": 60}, 200).Map(t)
	if Str(su, "download_path") == "" {
		t.Fatalf("signed-url without download_path: %v", su)
	}
	dl := admin.Do("GET", pa.Path("/storage/download?key="+url.QueryEscape("docs/a.bin")), nil)
	if dl.Status != 200 {
		t.Errorf("GAP/FAIL: GET /storage/download -> %d %s (download_path from signed-url points at a route the gateway does not expose)", dl.Status, truncate(string(dl.Body), 160))
	} else {
		if !bytes.Equal(dl.Body, payload) {
			t.Errorf("downloaded bytes differ (got %d, want %d)", len(dl.Body), len(payload))
		}
		if dl.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("download lacks X-Content-Type-Options: nosniff")
		}
	}
	if streamOK {
		d2 := admin.Do("GET", pa.Path("/storage/download?key="+url.QueryEscape("big/stream.bin")), nil)
		if d2.Status == 200 && !bytes.Equal(d2.Body, stream) {
			t.Errorf("streamed object corrupted on download (%d vs %d bytes)", len(d2.Body), len(stream))
		}
	}
	// active content is never served inline
	admin.Must(t, "POST", pa.Path("/storage/objects"), map[string]string{"key": "x/evil.html", "content_base64": base64.StdEncoding.EncodeToString([]byte("<script>alert(1)</script>")), "content_type": "text/html"}, 201)
	if d := admin.Do("GET", pa.Path("/storage/download?key=x/evil.html"), nil); d.Status == 200 {
		if !strings.HasPrefix(d.Header.Get("Content-Disposition"), "attachment") {
			t.Errorf("HTML object served inline: %q", d.Header.Get("Content-Disposition"))
		}
	}

	// ---- key validation: traversal, absolute, control chars, reserved prefix, length
	badKeys := []string{"../x", "a/../../x", "..", "/abs/path", "a//b", `a\b`, ".", "x/./y", "a\x00b", "a\nb", strings.Repeat("k", 1100), "", "_backups/evil", "_backups/"}
	for _, k := range badKeys {
		r := admin.Do("POST", pa.Path("/storage/objects"), map[string]string{"key": k, "content_base64": base64.StdEncoding.EncodeToString([]byte("x"))})
		if r.Status != 400 && r.Status != 403 {
			t.Errorf("POST key %q: want 400/403, got %d %s", truncate(k, 20), r.Status, truncate(string(r.Body), 100))
		}
		if streamOK {
			r = admin.DoRaw("PUT", pa.Path("/storage/objects?key="+url.QueryEscape(k)), []byte("x"), "text/plain", nil)
			if r.Status != 400 && r.Status != 403 {
				t.Errorf("PUT key %q: want 400/403, got %d", truncate(k, 20), r.Status)
			}
		}
		if r := admin.Do("GET", pa.Path("/storage/download?key="+url.QueryEscape(k)), nil); r.Status == 200 {
			t.Errorf("download of bad key %q returned 200", truncate(k, 20))
		}
	}

	// ---- oversize JSON upload is rejected (base64 limit is 50 MiB of content)
	huge := make([]byte, 53<<20)
	hr := admin.DoRaw("POST", pa.Path("/storage/objects"), []byte(`{"key":"huge.bin","content_base64":"`+base64.StdEncoding.EncodeToString(huge)+`"}`), "application/json", nil)
	if hr.Status != 413 {
		t.Errorf("53MiB JSON upload: want 413, got %d %s", hr.Status, truncate(string(hr.Body), 120))
	}
	if r := admin.Do("GET", pa.Path("/storage/objects?prefix=huge"), nil); r.Status == 200 && len(Arr(r.Map(t), "objects")) != 0 {
		t.Errorf("rejected upload left an object behind")
	}

	// ---- project isolation
	lb := admin.Must(t, "GET", pb.Path("/storage/objects"), nil, 200).Map(t)
	if len(Arr(lb, "objects")) != 0 {
		t.Errorf("project B sees project A objects: %v", Arr(lb, "objects"))
	}
	if d := admin.Do("GET", pb.Path("/storage/download?key=docs/a.bin"), nil); d.Status == 200 {
		t.Errorf("project B downloaded project A's object")
	}
	if r := admin.Do("POST", pb.Path("/storage/signed-url"), map[string]string{"key": "docs/a.bin"}); r.Status != 404 {
		t.Errorf("signed url for another project's key: want 404, got %d", r.Status)
	}
	// same key in B is independent
	admin.Must(t, "POST", pb.Path("/storage/objects"), map[string]string{"key": "docs/a.bin", "content_base64": base64.StdEncoding.EncodeToString([]byte("B-data"))}, 201)
	admin.Must(t, "DELETE", pb.Path("/storage/objects?key=docs/a.bin"), nil, 200, 204)
	if d := admin.Do("GET", pa.Path("/storage/download?key=docs/a.bin"), nil); d.Status == 200 && !bytes.Equal(d.Body, payload) {
		t.Errorf("deleting B's object affected A's object")
	}

	// ---- RBAC: a viewer-preset user has no storage area
	v := CreateUser(t, admin, org, "member", false, Grant{"project_id": pa.ID, "preset": "viewer"})
	if r := v.C.Do("GET", pa.Path("/storage/objects"), nil); r.Status != 403 {
		t.Errorf("viewer listing storage: want 403, got %d", r.Status)
	}
	ro := CreateUser(t, admin, org, "member", false, Grant{"project_id": pa.ID, "preset": "custom", "areas": map[string]string{"overview": "view", "storage": "view"}})
	if r := ro.C.Do("GET", pa.Path("/storage/objects"), nil); r.Status != 200 {
		t.Errorf("storage:view user listing: want 200, got %d", r.Status)
	}
	if r := ro.C.Do("POST", pa.Path("/storage/objects"), map[string]string{"key": "ro.txt", "content_base64": "eA=="}); r.Status != 403 {
		t.Errorf("storage:view user uploading: want 403, got %d", r.Status)
	}
	if r := ro.C.Do("DELETE", pa.Path("/storage/objects?key=docs/a.bin"), nil); r.Status != 403 {
		t.Errorf("storage:view user deleting: want 403, got %d", r.Status)
	}

	// ---- delete
	admin.Must(t, "DELETE", pa.Path("/storage/objects?key=docs/a.bin"), nil, 200, 204)
	lr = admin.Must(t, "GET", pa.Path("/storage/objects?prefix=docs/"), nil, 200).Map(t)
	if len(Arr(lr, "objects")) != 0 {
		t.Errorf("object still listed after delete: %v", Arr(lr, "objects"))
	}
	if d := admin.Do("GET", pa.Path("/storage/download?key=docs/a.bin"), nil); d.Status == 200 {
		t.Errorf("deleted object still downloadable")
	}
	if r := admin.Do("DELETE", pa.Path("/storage/objects?prefix=_backups/"), nil); r.Status != 403 {
		t.Errorf("prefix delete over reserved keys: want 403, got %d", r.Status)
	}
	admin.Must(t, "DELETE", pa.Path("/storage/objects?prefix=x/"), nil, 200, 204)
}
