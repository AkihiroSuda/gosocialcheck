package cncf

const ProjectsURL = "https://raw.githubusercontent.com/cncf/clomonitor/refs/heads/main/data/cncf.yaml"

type Projects []Project

type Project struct {
	Name         string       `yaml:"name,omitempty"`
	DisplayName  string       `yaml:"display_name,omitempty"`
	Description  string       `yaml:"description,omitempty"`
	Category     string       `yaml:"category,omitempty"`
	LogoURL      string       `yaml:"logo_url,omitempty"`
	DevstatsURL  string       `yaml:"devstats_url,omitempty"`
	AcceptedAt   string       `yaml:"accepted_at,omitempty"`
	Maturity     string       `yaml:"maturity,omitempty"`
	Repositories []Repository `yaml:"repositories,omitempty"`
}

type Repository struct {
	Name      string   `yaml:"name,omitempty"`
	URL       string   `yaml:"url,omitempty"`
	CheckSets []string `yaml:"check_sets,omitempty"`
}
