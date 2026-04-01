//go:build !unix

package app

import (
	"os"
	"syscall"
)

func checkSysProcAttr() *syscall.SysProcAttr {
	return nil
}

func interruptCheckProcess(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Kill()
}
