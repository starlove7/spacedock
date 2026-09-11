package policy

import "fmt"

type Permission string

const (
	PermissionFSRead          Permission = "fs.read"
	PermissionFSWrite         Permission = "fs.write"
	PermissionCommandExecute  Permission = "command.execute"
	PermissionGitRead         Permission = "git.read"
	PermissionGitWrite        Permission = "git.write"
	PermissionWorkspaceManage Permission = "workspace.manage"
	PermissionRecallRead      Permission = "recall.read"
	PermissionRecallWrite     Permission = "recall.write"
	PermissionACPConnect      Permission = "acp.connect"
	PermissionAgentExecute    Permission = "agent.execute"
)

func ParsePermission(s string) (Permission, error) {
	p := Permission(s)
	switch p {
	case PermissionFSRead, PermissionFSWrite, PermissionCommandExecute, PermissionGitRead, PermissionGitWrite, PermissionWorkspaceManage, PermissionRecallRead, PermissionRecallWrite, PermissionACPConnect, PermissionAgentExecute:
		return p, nil
	}
	return "", fmt.Errorf("unknown permission %q", s)
}
func ContainsPermission(ps []Permission, p Permission) bool {
	for _, x := range ps {
		if x == p {
			return true
		}
	}
	return false
}
