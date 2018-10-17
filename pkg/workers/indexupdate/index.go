package indexupdate

import (
	"fmt"
	"runtime"
	"time"

	"github.com/cozy/cozy-stack/pkg/fulltext/indexation"
	"github.com/cozy/cozy-stack/pkg/jobs"
)

func init() {

	fmt.Println("init called")

	jobs.AddWorker(&jobs.WorkerConfig{
		WorkerType:   "indexupdate",
		Concurrency:  runtime.NumCPU(),
		MaxExecCount: 1,
		Timeout:      1 * time.Minute,
		WorkerFunc:   Worker,
	})
}

// Worker is a worker that indexes all changes since last time (using couchdb seq)
func Worker(ctx *jobs.WorkerContext) error {

	fmt.Println("indexupdate worker working")
	return indexation.AllIndexesUpdate()

}
