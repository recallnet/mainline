//go:build !unix

package git

import (
	"os"
	"syscall"
)

func pushSysProcAttr() *syscall.SysProcAttr {
	return nil
}

func interruptProcess(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Kill()
}
