//go:build !linux

package service

import (
	"fmt"
	"io"
)

func unsupported() error     { return fmt.Errorf("systemd service management is supported only on Linux") }
func Install(string) error   { return unsupported() }
func Start() error           { return unsupported() }
func Stop() error            { return unsupported() }
func Restart() error         { return unsupported() }
func Status(io.Writer) error { return unsupported() }
func Uninstall() error       { return unsupported() }
