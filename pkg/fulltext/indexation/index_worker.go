package indexation

import (
	"errors"
	"fmt"
	"time"

	"github.com/cozy/cozy-stack/pkg/consts"
)

var updateQueue chan UpdateIndexNotif

var updateIndexRetryTimeMax = time.Minute * 10

var updateIndexRetryCountMax = 5

var updateQueueSize = 100

func StartWorker() {

	updateQueue = make(chan UpdateIndexNotif, updateQueueSize)

	go func(updateQueue <-chan UpdateIndexNotif) {
		for notif := range updateQueue {

			err := indexController.UpdateIndex(notif.InstanceName, notif.DocType)
			if err != nil {
				fmt.Printf("Error on UpdateIndex for %s instance and %s doctype: %s\n", notif.InstanceName, notif.DocType, err)
				// We retry the indexation after an indexUpdateRetryTime
				go RetryJob(notif)
			} else {
				// Send the new index to the search side
				err := indexController.SendIndexToQuery(notif.InstanceName, notif.DocType) // TODO: deal with errors
				if err != nil {
					fmt.Printf("Error on sendIndexToQuery: %s\n", err)
					go RetryJob(notif)
				}
				if notif.DocType == consts.Files {
					// Also send content to query side
					err := indexController.SendIndexToQuery(notif.InstanceName, ContentType)
					if err != nil {
						fmt.Printf("Error on sendIndexToQuery: %s\n", err)
						go RetryJob(notif)
					}
				}
			}
		}
	}(updateQueue)
}

func RetryJob(updateNotif UpdateIndexNotif) {
	timer := time.NewTimer(updateIndexRetryTimeMax)
	<-timer.C
	updateNotif.RetryCount += 1
	err := AddUpdateIndexJob(updateNotif)
	if err != nil {
		fmt.Printf("Error on AddUpdateIndexJob: %s\n", err)
	}
}

func AddUpdateIndexJob(updateNotif UpdateIndexNotif) error {

	if updateNotif.RetryCount > updateIndexRetryCountMax {
		return errors.New("RetryCount has exceeded updateIndexRetryCountMax for " + updateNotif.DocType + "doctype")
	}

	select {
	case updateQueue <- updateNotif:
		return nil
	default:
		return errors.New("Update Queue is full, can't add new doctype to the update queue for now")
	}
}
