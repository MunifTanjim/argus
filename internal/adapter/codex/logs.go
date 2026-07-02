package codex

import (
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func logsDBPath() (string, error) {
	dir, err := codexHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "logs_2.sqlite"), nil
}

// Codex encodes pid as "pid:<pid>:<uuid>" in process_uuid.
func lastProcessPID(threadID string) (pid int, ok bool) {
	path, err := logsDBPath()
	if err != nil {
		return 0, false
	}
	if _, err := os.Stat(path); err != nil {
		return 0, false
	}
	db, err := openCodexDB(path)
	if err != nil {
		return 0, false
	}
	defer db.Close()

	var procUUID sql.NullString
	err = db.QueryRow(`SELECT process_uuid FROM logs
		WHERE thread_id = ? AND process_uuid IS NOT NULL
		ORDER BY ts DESC, ts_nanos DESC, id DESC LIMIT 1`, threadID).Scan(&procUUID)
	if err != nil || !procUUID.Valid {
		return 0, false
	}
	return parseProcessPID(procUUID.String)
}

func parseProcessPID(procUUID string) (int, bool) {
	parts := strings.SplitN(procUUID, ":", 3)
	if len(parts) < 2 || parts[0] != "pid" {
		return 0, false
	}
	pid, err := strconv.Atoi(parts[1])
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}
