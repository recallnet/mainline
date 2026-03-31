//go:build unix

package git

import "syscall"

func pushSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

func interruptProcess(pid int) error {
	return syscall.Kill(-pid, syscall.SIGKILL)
}
