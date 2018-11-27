package indexation

import (
	"fmt"

	"github.com/cozy/cozy-stack/pkg/instance"
)

type UpdateIndexNotif struct {
	InstanceName string
	DocType      string
	RetryCount   int
}

type OptionsIndex struct {
	Highlight bool `yaml:highlight`
	Content   bool `yaml:content`
}

const (
	prefixPath  = "bleve/index/"
	ContentType = "io.cozy.files.content"
)

// var indexes map[string]*InstanceIndex

var indexController IndexController

var ft_language *FastText

func StartIndex(instanceList []*instance.Instance) error {

	ft_language = NewFastTextInst()
	ft_language.LoadModel("pkg/fulltext/indexation/lid.176.ftz")

	languages := GetAvailableLanguages()

	err := indexController.Init(instanceList, languages)
	if err != nil {
		fmt.Printf("Error on init indexController: %s\n", err)
		return err
	}

	StartWorker()

	return indexController.UpdateAllIndexes()
}

func getContentFile(uuid string, id string) (string, error) {
	// This is a mock function
	return "hello world", nil
}

func ReIndexAll(instName string) error {
	return indexController.ReIndexAll(instName)
}

func ReIndex(instName string, docType string) error {
	return indexController.ReIndex(instName, docType)
}

func UpdateAllIndexes() error {
	return indexController.UpdateAllIndexes()
}

func ReplicateAll(instName string) error {
	return indexController.ReplicateAll(instName)
}

func Replicate(instName string, docType string, lang string) (string, error) {
	return indexController.Replicate(instName, docType, lang)
}

func DeleteIndex(instName string, docType string, querySide bool) error {
	return indexController.DeleteIndex(instName, docType, querySide)
}

func DeleteAllIndexesInstance(instName string, querySide bool) error {
	return indexController.DeleteAllIndexesInstance(instName, querySide)
}

func GetMappingVersion(instName, docType, lang string) (string, error) {
	return indexController.GetMappingVersion(instName, docType, lang)
}

func SetOptionsInstance(instName string, options map[string]bool) (map[string]bool, error) {
	return indexController.SetOptionsInstance(instName, options)
}
