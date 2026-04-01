//go:build unix

package app

import "syscall"

func checkSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

func interruptCheckProcess(pid int) error {
	return syscall.Kill(-pid, syscall.SIGKILL)
}
