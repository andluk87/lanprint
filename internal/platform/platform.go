package platform

type Adapter struct {
	Physical bool `json:"physical"`
	Preferred bool `json:"preferred"`
	Name        string `json:"name"`
	Description string `json:"description"`
	MAC         string `json:"mac"`
	Address     string `json:"address"`
	Mask        string `json:"mask"`
	Gateway     string `json:"gateway"`
	Up          bool   `json:"up"`
	DHCP        bool   `json:"dhcp"`
}
type Printer struct {
	Name   string `json:"name"`
	Driver string `json:"driver"`
	Port   string `json:"port"`
	Status string `json:"status"`
}
