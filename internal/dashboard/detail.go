package dashboard

import (
	"time"

	"syslog-gomonitor/internal/human"
	"syslog-gomonitor/internal/snapshot"
	"syslog-gomonitor/internal/storage"
)

// DeviceDetail is the card opened from a device row. It is metadata only.
type DeviceDetail struct {
	Name         string       `json:"name"`
	SourceIP     string       `json:"source_ip"`
	Transport    string       `json:"transport"`
	Silence      string       `json:"silence"`
	Status       string       `json:"status"`
	StatusClass  string       `json:"status_class"`
	LogLabel     string       `json:"log_label"`
	LogDetail    string       `json:"log_detail"`
	Connection   string       `json:"connection"`
	LogPath      string       `json:"log_path"`
	LastWrite    string       `json:"last_write"`
	Active       string       `json:"active"`
	Stored       string       `json:"stored"`
	StoredDetail string       `json:"stored_detail"`
	Directory    string       `json:"directory"`
	Files        []DetailFile `json:"files"`
	Note         string       `json:"note,omitempty"`
}

// DetailFile is one name in the device's log directory.
type DetailFile struct {
	Name string `json:"name"`
	Size string `json:"size"`
	When string `json:"when"`
	Kind string `json:"kind"`
}

// Detail builds the device card from the latest status row and a directory listing.
func Detail(device snapshot.Device, listing storage.Listing) DeviceDetail {
	active, _ := activeView(device.ActiveBytes)
	stored, storedDetail, _ := storedView(device)
	files := make([]DetailFile, 0, len(listing.Files))
	for _, file := range listing.Files {
		size := "Not followed"
		if file.Kind != "symlink" && file.Kind != "directory" {
			size = human.Bytes(file.Bytes)
		}
		when := ""
		if !file.ModTime.IsZero() {
			utc := file.ModTime.UTC()
			when = clock(&utc)
		}
		files = append(files, DetailFile{
			Name: file.Name,
			Size: size,
			When: when,
			Kind: file.Kind,
		})
	}
	return DeviceDetail{
		Name:         device.Name,
		SourceIP:     device.SourceIP,
		Transport:    transport(device),
		Silence:      human.Duration(time.Duration(device.StaleAfterSeconds) * time.Second),
		Status:       label(statusLabels, device.Status),
		StatusClass:  device.Status,
		LogLabel:     label(logLabels, device.LogStatus),
		LogDetail:    device.LogDetail,
		Connection:   device.ConnectionDetail,
		LogPath:      device.LogPath,
		LastWrite:    clock(device.LastWrite),
		Active:       active,
		Stored:       stored,
		StoredDetail: storedDetail,
		Directory:    listing.Directory,
		Files:        files,
		Note:         listing.Note,
	}
}
