package handlers

import "github.com/jp-cloud/runtime/internal/dockerx"

func dockerSpec(name, org, proj string) dockerx.ContainerSpec {
	return dockerx.ContainerSpec{Name: name, Image: "x/y:1", Network: "jp_p_zzzz",
		Labels: map[string]string{"jp.managed": "true", "jp.org": org, "jp.project": proj, "jp.kind": "app"}}
}
