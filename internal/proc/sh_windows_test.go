//go:build windows

package proc

import "errors"

func lookSh() (string, error) { return "", errors.New("no POSIX shell on Windows") }
