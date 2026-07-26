package traefikcfg

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Writer manages per-domain Traefik file-provider configs.
type Writer struct {
	Dir            string
	BackendURL     string // e.g. http://gateway:8000 or runtime target
	CertResolver   string
}

type dynamicFile struct {
	HTTP httpBlock `yaml:"http"`
}

type httpBlock struct {
	Routers  map[string]router  `yaml:"routers"`
	Services map[string]service `yaml:"services"`
}

type router struct {
	Rule        string   `yaml:"rule"`
	EntryPoints []string `yaml:"entryPoints"`
	Service     string   `yaml:"service"`
	TLS         *tlsCfg  `yaml:"tls,omitempty"`
}

type tlsCfg struct {
	CertResolver string `yaml:"certResolver,omitempty"`
}

type service struct {
	LoadBalancer lb `yaml:"loadBalancer"`
}

type lb struct {
	Servers []server `yaml:"servers"`
}

type server struct {
	URL string `yaml:"url"`
}

func New(dir, backendURL, certResolver string) *Writer {
	if dir == "" {
		dir = os.Getenv("TRAEFIK_DYNAMIC_DIR")
	}
	if dir == "" {
		dir = "/etc/traefik/dynamic"
	}
	if backendURL == "" {
		backendURL = os.Getenv("TRAEFIK_BACKEND_URL")
	}
	if backendURL == "" {
		backendURL = "http://gateway:8000"
	}
	if certResolver == "" {
		certResolver = os.Getenv("TRAEFIK_CERT_RESOLVER")
	}
	return &Writer{Dir: dir, BackendURL: backendURL, CertResolver: certResolver}
}

func (w *Writer) WriteDomain(hostname, projectID string, enableTLS bool) (filename string, err error) {
	if err := os.MkdirAll(w.Dir, 0o755); err != nil {
		return "", err
	}
	safe := sanitize(hostname)
	filename = filepath.Join(w.Dir, "domain-"+safe+".yml")
	name := "jp-" + safe
	r := router{
		Rule:        fmt.Sprintf("Host(`%s`)", hostname),
		EntryPoints: []string{"web"},
		Service:     name,
	}
	if enableTLS && w.CertResolver != "" {
		r.EntryPoints = []string{"web", "websecure"}
		r.TLS = &tlsCfg{CertResolver: w.CertResolver}
	}
	doc := dynamicFile{
		HTTP: httpBlock{
			Routers: map[string]router{name: r},
			Services: map[string]service{
				name: {LoadBalancer: lb{Servers: []server{{URL: w.BackendURL}}}},
			},
		},
	}
	raw, err := yaml.Marshal(doc)
	if err != nil {
		return "", err
	}
	header := fmt.Sprintf("# jp auto-generated for project %s domain %s\n", projectID, hostname)
	if err := os.WriteFile(filename, append([]byte(header), raw...), 0o644); err != nil {
		return "", err
	}
	return filename, nil
}

func (w *Writer) RemoveDomain(hostname string) error {
	safe := sanitize(hostname)
	path := filepath.Join(w.Dir, "domain-"+safe+".yml")
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func sanitize(host string) string {
	h := strings.ToLower(host)
	h = strings.ReplaceAll(h, ".", "-")
	h = strings.ReplaceAll(h, "*", "star")
	return h
}
