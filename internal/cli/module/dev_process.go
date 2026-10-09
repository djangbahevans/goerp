package module

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"time"
)

type devProcess struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error
}

type devProcesses struct {
	ctx      context.Context
	cancel   context.CancelCauseFunc
	children []*devProcess
	stdout   io.Writer
	stderr   io.Writer
}

func (s *devProcesses) start(dir string, env []string, stdout io.Writer, background bool, name string, args ...string) (*devProcess, error) {
	if err := s.ctx.Err(); err != nil {
		return nil, context.Cause(s.ctx)
	}

	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = stdout
	cmd.Stderr = s.stderr
	if err := devProcessGroup(cmd); err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", name, err)
	}

	p := &devProcess{cmd: cmd, done: make(chan struct{})}
	s.children = append(s.children, p)
	go func() {
		p.err = cmd.Wait()
		close(p.done)
		if background {
			if p.err != nil {
				s.cancel(fmt.Errorf("%s exited unexpectedly: %w", name, p.err))
			} else {
				s.cancel(fmt.Errorf("%s exited before the dev session stopped", name))
			}
		}
	}()

	return p, nil
}

func (s *devProcesses) run(dir string, env []string, stdout io.Writer, name string, args ...string) error {
	p, err := s.start(dir, env, stdout, false, name, args...)
	if err != nil {
		return err
	}
	defer func() {
		p.stop()
		s.children = slices.DeleteFunc(s.children, func(child *devProcess) bool { return child == p })
	}()

	select {
	case <-s.ctx.Done():
		return context.Cause(s.ctx)
	case <-p.done:
		if p.err != nil {
			return fmt.Errorf("run %s: %w", name, p.err)
		}
		return nil
	}
}

func (s *devProcesses) stop() {
	for _, p := range slices.Backward(s.children) {
		p.stop()
	}
	s.children = nil
	s.cancel(context.Canceled)
}

func (p *devProcess) stop() {
	_ = devSignalGroup(p.cmd.Process.Pid, false)
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()

	select {
	case <-p.done:
	case <-timer.C:
	}
	// A wrapper may exit before its descendants; kill the remaining process group too.
	_ = devSignalGroup(p.cmd.Process.Pid, true)
	<-p.done
}
