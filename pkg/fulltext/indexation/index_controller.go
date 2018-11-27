package indexation

import (
	"errors"
	"fmt"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"os"
	"sync"

	"github.com/cozy/cozy-stack/pkg/instance"
)

type IndexController struct {
	indexes   map[string]*InstanceIndex
	languages []string
}

// The indexController is the interface to use to manipulate the indexes.
// It is responsible for checking/creating the appropriate instanceIndexes
// and assuring mutual exclusion when necessary for functions underneath.

func (indexController *IndexController) Init(instanceList []*instance.Instance, languages []string) error {
	indexController.setLanguages(languages)

	indexController.indexes = make(map[string]*InstanceIndex)
	for _, inst := range instanceList {
		err := indexController.initializeIndexes(inst.DomainName())
		if err != nil {
			return err
		}
	}

	return nil
}

func (indexController *IndexController) initializeIndexes(instName string) error {

	options, err := indexController.GetOptionsInstance(instName)

	indexController.indexes[instName] = &InstanceIndex{
		make(map[string]map[string]*IndexWrapper),
		new(sync.Mutex),
		options.Highlight,
		options.Content,
		instName,
	}

	docTypeList, err := GetDocTypeListFromDescriptionFile()
	if err != nil {
		return err
	}

	instanceIndex, _ := indexController.getInstanceIndex(instName, false)
	if err != nil {
		fmt.Printf("Error on GetOptionsInstance", err)
	}

	instanceIndex.lockInstance()
	defer instanceIndex.unlockInstance()

	for _, docType := range docTypeList {
		err = instanceIndex.initializeIndexDocType(docType)
		if err != nil {
			return err
		}
	}

	return nil
}

func (indexController *IndexController) getInstanceIndex(instName string, force bool) (*InstanceIndex, error) {
	if force {
		err := indexController.makeSureInstanceReady(instName)
		if err != nil {
			return nil, err
		}
	} else {
		err := indexController.checkInstance(instName)
		if err != nil {
			return nil, err
		}
	}

	return indexController.indexes[instName], nil
}

func (indexController *IndexController) GetLanguages() []string {
	return indexController.languages
}

func (indexController *IndexController) setLanguages(languages []string) {
	indexController.languages = languages
}

func (indexController *IndexController) makeSureInstanceReady(instName string) error {
	err := indexController.checkInstance(instName)
	if err != nil {
		// Not existing already, try to initialize it (for mutex)
		err = indexController.initializeIndexes(instName)
		if err != nil {
			return err
		}
	}
	return nil
}

func (indexController *IndexController) checkInstance(instName string) error {
	if _, ok := indexController.indexes[instName]; !ok {
		return errors.New("Instance not found in CheckInstance")
	}
	return nil
}

func (indexController *IndexController) GetOptionsInstance(instName string) (OptionsIndex, error) {
	options := OptionsIndex{false, false}

	data, err := ioutil.ReadFile(prefixPath + instName + "/config.yml")
	if err != nil {
		// We return default
		return options, nil
	}

	err = yaml.Unmarshal([]byte(data), &options)
	if err != nil {
		return options, err
	}

	return options, nil
}

func (indexController *IndexController) SetOptionsInstance(instName string, options map[string]bool) (map[string]bool, error) {

	instanceIndex, err := indexController.getInstanceIndex(instName, true)
	if err != nil {
		return nil, err
	}

	instanceIndex.lockInstance()
	defer instanceIndex.unlockInstance()

	return instanceIndex.SetOptionsInstance(options)
}

func (indexController *IndexController) GetMappingVersion(instName, docType, lang string) (string, error) {
	instanceIndex, err := indexController.getInstanceIndex(instName, false)
	if err != nil {
		return "", err
	}

	instanceIndex.lockInstance()
	defer instanceIndex.unlockInstance()

	err = instanceIndex.checkInstanceDocType(docType)
	if err != nil {
		return "", err
	}

	err = instanceIndex.checkInstanceDocTypeLang(docType, lang)
	if err != nil {
		return "", err
	}

	return instanceIndex.GetMappingVersion(docType, lang)
}

func (indexController *IndexController) UpdateAllIndexes() error {

	docTypeList, err := GetDocTypeListFromDescriptionFile()
	if err != nil {
		return err
	}

	for instName := range indexController.indexes {
		for _, docType := range docTypeList {
			err := AddUpdateIndexJob(UpdateIndexNotif{instName, docType, 0})
			if err != nil {
				fmt.Printf("Could not add update index job: %s\n", err)
			}
		}
	}
	return nil
}

func (indexController *IndexController) ReIndexAll(instName string) error {

	docTypeList, err := GetDocTypeListFromDescriptionFile()
	if err != nil {
		return err
	}

	instanceIndex, err := indexController.getInstanceIndex(instName, true)
	if err != nil {
		return err
	}

	instanceIndex.lockInstance()
	defer instanceIndex.unlockInstance()

	for _, docType := range docTypeList {
		err := instanceIndex.ReIndex(docType)
		if err != nil {
			return err
		}
	}

	return nil
}

func (indexController *IndexController) ReIndex(instName string, docType string) error {

	instanceIndex, err := indexController.getInstanceIndex(instName, true)
	if err != nil {
		return err
	}

	instanceIndex.lockInstance()
	defer instanceIndex.unlockInstance()

	return instanceIndex.ReIndex(docType)
}

func (indexController *IndexController) ReplicateAll(instName string) error {

	err := indexController.checkInstance(instName)
	if err != nil {
		return err
	}

	instanceIndex, err := indexController.getInstanceIndex(instName, false)
	if err != nil {
		return err
	}

	instanceIndex.lockInstance()
	defer instanceIndex.unlockInstance()

	return instanceIndex.ReplicateAll()
}

func (indexController *IndexController) Replicate(instName string, docType string, lang string) (string, error) {

	instanceIndex, err := indexController.getInstanceIndex(instName, false)
	if err != nil {
		return "", err
	}

	instanceIndex.lockInstance()
	defer instanceIndex.unlockInstance()

	err = instanceIndex.checkInstanceDocType(docType)
	if err != nil {
		return "", err
	}

	err = instanceIndex.checkInstanceDocTypeLang(docType, lang)
	if err != nil {
		return "", err
	}

	return instanceIndex.Replicate(docType, lang)
}

func (indexController *IndexController) DeleteAllIndexesInstance(instName string, querySide bool) error {
	instanceIndex, err := indexController.getInstanceIndex(instName, false)
	if err != nil {
		return err
	}

	instanceIndex.lockInstance()
	defer instanceIndex.unlockInstance()

	err = instanceIndex.DeleteAllIndexes(querySide)
	if err != nil {
		return err
	}

	delete(indexController.indexes, instName)
	return os.RemoveAll(prefixPath + instName)
}

func (indexController *IndexController) DeleteIndex(instName string, docType string, querySide bool) error {
	instanceIndex, err := indexController.getInstanceIndex(instName, false)
	if err != nil {
		return err
	}

	instanceIndex.lockInstance()
	defer instanceIndex.unlockInstance()

	err = instanceIndex.checkInstanceDocType(docType)
	if err != nil {
		return err
	}

	return instanceIndex.deleteIndex(docType, querySide)
}

func (indexController *IndexController) SendIndexToQuery(instName string, docType string) error {
	instanceIndex, err := indexController.getInstanceIndex(instName, false)
	if err != nil {
		return err
	}

	instanceIndex.lockInstance()
	defer instanceIndex.unlockInstance()

	err = instanceIndex.checkInstanceDocType(docType)
	if err != nil {
		return err
	}

	for _, lang := range indexController.GetLanguages() {
		err := instanceIndex.sendIndexToQuery(docType, lang)
		if err != nil {
			return err
		}
	}
	return nil
}

func (indexController *IndexController) UpdateIndex(instName string, docType string) error {

	instanceIndex, err := indexController.getInstanceIndex(instName, true)
	if err != nil {
		return err
	}

	instanceIndex.lockInstance()
	defer instanceIndex.unlockInstance()

	err = instanceIndex.makeSureInstanceDocTypeReady(docType)
	if err != nil {
		fmt.Printf("Error on makeSureInstanceDocTypeReady: %s\n", err)
		return err
	}

	var indexUpdater IndexUpdater

	indexUpdater.init(instanceIndex, docType)

	return indexUpdater.UpdateIndex()
}
