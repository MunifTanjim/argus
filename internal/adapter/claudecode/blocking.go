package claudecode

import "github.com/MunifTanjim/argus/internal/adapter"

func ShouldBlock(ev adapter.HookEvent) bool { return EventName(ev) == "PermissionRequest" }
