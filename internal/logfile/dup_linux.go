package logfile

import "golang.org/x/sys/unix"

// linux/arm64 has no dup2.
func dupOnto(oldfd, newfd int) error {
	return unix.Dup3(oldfd, newfd, 0)
}
