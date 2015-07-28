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
