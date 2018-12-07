package indexation

import (
	"errors"
	"fmt"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"os"
	"path"
	"sync"

	"github.com/cozy/cozy-stack/pkg/instance"
)

type IndexController struct {
	indexes map[string]*InstanceIndex
}

type OptionsIndex struct {
	Highlight bool     `yaml:"highlight" json:"highlight"`
	Content   bool     `yaml:"content" json:"content"`
	Languages []string `yaml:"languages" json:"languages"`
}

// The indexController is the interface to use to manipulate the indexes.
// It is responsible for checking/creating the appropriate instanceIndexes
// and assuring mutual exclusion when necessary for functions underneath.

func (indexController *IndexController) Init(instanceList []*instance.Instance) error {
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
		options.Languages,
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

	err = instanceIndex.WriteOptionsInstance(options)
	if err != nil {
		return err
	}

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

func (indexController *IndexController) GetOptionsInstance(instName string) (*OptionsIndex, error) {
	options := &OptionsIndex{false, false, []string{defaultLanguage}}

	data, err := ioutil.ReadFile(path.Join(prefixPath, instName, "config.yml"))
	if err != nil {
		// We return default options
		return options, nil
	}

	err = yaml.Unmarshal([]byte(data), &options)
	if err != nil {
		return nil, err
	}

	return options, nil
}

func (indexController *IndexController) SetOptionsInstance(instName string, options map[string]interface{}) (*OptionsIndex, error) {

	instanceIndex, err := indexController.getInstanceIndex(instName, true)
	if err != nil {
		return nil, err
	}

	instanceIndex.lockInstance()
	defer instanceIndex.unlockInstance()

	prevOptions, err := indexController.GetOptionsInstance(instName)
	if err != nil {
		return nil, err
	}

	if content, ok := options["content"]; ok {
		prevOptions.Content = content.(bool)
	}

	if highlight, ok := options["highlight"]; ok {
		prevOptions.Highlight = highlight.(bool)
	}

	if languages, ok := options["languages"]; ok {
		if len(languages.([]string)) == 0 {
			return nil, errors.New("languages can't be empty")
		}
		prevOptions.Languages = languages.([]string)
	}

	return instanceIndex.SetOptionsInstance(prevOptions)
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
	return os.RemoveAll(path.Join(prefixPath, instName))
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

	for _, lang := range instanceIndex.getLanguages() {
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

	if len(instanceIndex.getLanguages()) == 0 {
		return errors.New("Error on UpdateIndex: No language found for this instance")
	}

	var indexUpdater IndexUpdater

	indexUpdater.Init(instanceIndex, docType)

	return indexUpdater.UpdateIndex()
}
