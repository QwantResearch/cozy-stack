package instance

import (
	"github.com/cozy/cozy-stack/pkg/jobs"
	"github.com/cozy/cozy-stack/pkg/prefixer"
)

// Triggers returns the list of the triggers to add when an instance is created
func Triggers(db prefixer.Prefixer) []jobs.TriggerInfos {
	return []jobs.TriggerInfos{
		// Create/update/remove thumbnails when an image is created/updated/removed
		{
			Domain:     db.DomainName(),
			Prefix:     db.DBPrefix(),
			Type:       "@event",
			WorkerType: "thumbnail",
			Arguments:  "io.cozy.files:CREATED,UPDATED,DELETED:image:class",
		},
		// Index all changes since last couchdb sequence every 2 min
		{
			Domain:     db.DomainName(),
			Prefix:     db.DBPrefix(),
			Type:       "@every",
			WorkerType: "indexupdate",
			Arguments:  "2m",
		},
	}
}
