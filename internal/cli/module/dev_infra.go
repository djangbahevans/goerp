package module

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

var devServices = []string{"postgres", "pgbouncer", "redis", "mailpit"}

type devContainer struct {
	Service    string `json:"Service"`
	State      string `json:"State"`
	Publishers []struct {
		PublishedPort int `json:"PublishedPort"`
	} `json:"Publishers"`
}

func (s *devProcesses) compose(repo string, stdout io.Writer, args ...string) error {
	base := []string{"compose", "--project-name", "goerp", "-f", filepath.Join(repo, "compose.dev.yml")}
	return s.run(repo, nil, stdout, "docker", append(base, args...)...)
}

func (s *devProcesses) startInfra(repo string) error {
	var config bytes.Buffer
	if err := s.compose(repo, &config, "config", "--format", "json"); err != nil {
		return err
	}
	var topology struct {
		Services map[string]struct {
			Ports []struct {
				Published string `json:"published"`
			} `json:"ports"`
		} `json:"services"`
	}
	if err := json.Unmarshal(config.Bytes(), &topology); err != nil {
		return fmt.Errorf("decode compose topology: %w", err)
	}

	var running bytes.Buffer
	if err := s.compose(repo, &running, "ps", "--format", "json"); err != nil {
		return err
	}
	containers, err := devContainers(running.Bytes())
	if err != nil {
		return err
	}
	for _, service := range devServices {
		definition, ok := topology.Services[service]
		if !ok {
			return fmt.Errorf("compose.dev.yml has no %s service", service)
		}
		for _, binding := range definition.Ports {
			port, err := strconv.Atoi(binding.Published)
			if err != nil {
				return fmt.Errorf("invalid published port %q for %s", binding.Published, service)
			}
			if devPortOwned(containers, service, port) {
				continue
			}
			if err := checkDevPort(s.ctx, port); err != nil {
				return err
			}
		}
	}

	args := append([]string{"up", "-d", "--wait", "--wait-timeout", "120", "--no-recreate"}, devServices...)
	return s.compose(repo, s.stdout, args...)
}

func devContainers(data []byte) ([]devContainer, error) {
	var containers []devContainer
	decoder := jsontext.NewDecoder(bytes.NewReader(data))
	for {
		value, err := decoder.ReadValue()
		if errors.Is(err, io.EOF) {
			return containers, nil
		}
		if err != nil {
			return nil, fmt.Errorf("decode running containers: %w", err)
		}
		var container devContainer
		if err := json.Unmarshal(value, &container); err != nil {
			return nil, fmt.Errorf("decode running container: %w", err)
		}
		containers = append(containers, container)
	}
}

func devPortOwned(containers []devContainer, service string, port int) bool {
	for _, container := range containers {
		if container.Service != service || container.State != "running" {
			continue
		}
		for _, binding := range container.Publishers {
			if binding.PublishedPort == port {
				return true
			}
		}
	}
	return false
}

func checkDevPort(ctx context.Context, port int) error {
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", ":"+strconv.Itoa(port))
	if err == nil {
		return listener.Close()
	}

	owner := "PID unavailable"
	if output, lookupErr := exec.CommandContext(ctx, "lsof", "-nP", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-t").Output(); lookupErr == nil {
		owner = "PID " + strings.Join(strings.Fields(string(output)), ", ")
	} else if output, lookupErr := exec.CommandContext(ctx, "ss", "-ltnp", "sport = :"+strconv.Itoa(port)).Output(); lookupErr == nil {
		if _, suffix, ok := strings.Cut(string(output), "pid="); ok {
			pid, _, _ := strings.Cut(suffix, ",")
			owner = "PID " + pid
		}
	}

	return fmt.Errorf("port %d is unavailable (%s): %w", port, owner, err)
}
