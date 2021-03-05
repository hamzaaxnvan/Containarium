package ospkg

type debianPkgMgr struct{}

func (d *debianPkgMgr) UpdateCmd() []string {
	return []string{"apt-get", "update"}
}

func (d *debianPkgMgr) InstallCmd(pkgs []string) []string {
	cmd := []string{"apt-get", "install", "-y"}
	return append(cmd, pkgs...)
}

func (d *debianPkgMgr) CreateUserCmd(username, gecos string) []string {
	if gecos == "" {
		gecos = ""
	}
	return []string{"adduser", "--disabled-password", "--gecos", gecos, username}
}

func (d *debianPkgMgr) SudoGroup() string {
	return "sudo"
}

func (d *debianPkgMgr) SSHServiceName() string {
	return "ssh"
}

func (d *debianPkgMgr) BasePackages() []string {
	return []string{
		"openssh-server",
		"sudo",
		"curl",
		"git",
		"vim",
