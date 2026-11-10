// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dsx

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"sigs.k8s.io/yaml"

	"github.com/dsx-ai-factory/nvke/controller/test/e2e/internal/environment"
	"github.com/dsx-ai-factory/nvke/controller/test/e2e/internal/infra"
)

// The checked-in environments/dsx.yaml, read the way a run reads it.
//
// Both failures this guards were in the file rather than in Go, so no Go test
// could have caught them, and both cost a bare-metal run to find:
//
//   - nodeUser defaulted to dgxc-admin, the account the manual procedure's
//     internal image bakes. The lane boots the OSS os-image, which bakes
//     ubuntu, so cloud-init wrote authorized_keys for an account that did not
//     exist and the dial was refused for its whole 35-minute budget.
//   - machines pinned one host, so a leftover instance on it failed every run
//     with 409 while the rest of the datahall sat free.
//
// Unset environment variables expand to empty, which is what a developer
// checkout has, so this asserts the shape of the file rather than any
// identifier in it.
func TestShippedEnvironmentLeavesPlacementAndAccountToTheDefaults(t *testing.T) {
	env, err := environment.LoadPath("dsx", filepath.Join("..", "..", "..", "environments", "dsx.yaml"))
	if err != nil {
		t.Fatalf("reading the shipped environments/dsx.yaml: %v", err)
	}
	decoded, err := Decode(env.Infrastructure)
	if err != nil {
		t.Fatalf("decoding it as the dsx dialect: %v", err)
	}
	if len(decoded.Sites) == 0 {
		t.Fatal("the shipped file declares no sites")
	}

	for key, site := range decoded.Sites {
		if site.NodeUser != "" {
			t.Errorf("site %q pins nodeUser=%q; leave it empty so it follows the image the run boots "+
				"(the OSS os-image bakes %q)", key, site.NodeUser, "ubuntu")
		}
		if len(site.Machines) != 0 {
			t.Errorf("site %q pins %d machine(s); leave machines out so the datahall picks one that is "+
				"free and healthy", key, len(site.Machines))
		}
		if len(site.InstanceTypes) == 0 {
			t.Errorf("site %q declares no instanceTypes; they place the tenant nodes and, with no machine "+
				"pinned, the site controller too", key)
		}
	}
}

// Every DSX stream in release/policy.yaml has to resolve against the shipped
// environment. The two files are edited apart and nothing else reads them
// together, so a stream asking for a gpu no site declares passes the plan and
// then fails the run with "no declared site offers", after a machine is wanted.
//
// Every variable the file names is set, so each site validates: an unset one
// skips its site, which would read as a stream with nowhere to run.
func TestShippedStreamsResolveAgainstTheShippedEnvironment(t *testing.T) {
	file := filepath.Join("..", "..", "..", "environments", "dsx.yaml")
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range regexp.MustCompile(`\$\{([A-Z0-9_]+)`).FindAllSubmatch(raw, -1) {
		t.Setenv(string(m[1]), "set")
	}
	env, err := environment.LoadPath("dsx", file)
	if err != nil {
		t.Fatalf("reading the shipped environments/dsx.yaml: %v", err)
	}
	decoded, err := Decode(env.Infrastructure)
	if err != nil {
		t.Fatalf("decoding it as the dsx dialect: %v", err)
	}

	policy, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "..", "release", "policy.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		Spec struct {
			Streams []struct {
				Name         string            `json:"name"`
				Capabilities map[string]string `json:"capabilities"`
			} `json:"streams"`
		} `json:"spec"`
	}
	if err := yaml.Unmarshal(policy, &p); err != nil {
		t.Fatalf("reading release/policy.yaml: %v", err)
	}

	streams := 0
	for _, s := range p.Spec.Streams {
		if s.Capabilities["provider"] != ProviderName {
			continue
		}
		streams++
		// What qualify-e2e.sh passes the suite: the architecture, and the gpu
		// when the stream names one.
		request := map[string]string{infra.CapabilityArch: s.Capabilities[infra.CapabilityArch]}
		if gpu := s.Capabilities[infra.CapabilityGPU]; gpu != "" {
			request[infra.CapabilityGPU] = gpu
		}
		if _, err := decoded.Resolve(request); err != nil {
			t.Errorf("stream %s resolves to nothing in environments/dsx.yaml: %v", s.Name, err)
		}
	}
	if streams == 0 {
		t.Fatal("release/policy.yaml names no dsx stream")
	}
}
