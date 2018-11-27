package indexation

import (
	"bytes"
	"errors"
	"fmt"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"net/http"
	"os"
	"sync"

	"github.com/blevesearch/bleve"
	"github.com/cozy/cozy-stack/client/request"
	"github.com/cozy/cozy-stack/pkg/consts"
	"github.com/cozy/cozy-stack/pkg/instance"
)

type InstanceIndex struct {
	indexList      map[string]map[string]*IndexWrapper
	indexMu        *sync.Mutex
	indexHighlight bool
	indexContent   bool
	instName       string
}

// All methods from instanceIndex must be called inside a mutex if necessary
// Additionally, all checks must have been done prior to calling any function

func (instanceIndex *InstanceIndex) initializeIndexDocType(docType string) error {

	var err error
	var created bool
	instanceIndex.indexList[docType] = make(map[string]*IndexWrapper, len(indexController.GetLanguages()))
	for _, lang := range indexController.GetLanguages() {
		instanceIndex.indexList[docType][lang], created, err = instanceIndex.getIndex(docType, lang)
		if err != nil {
			// It failed, we remove the erroneous doctype
			instanceIndex.deleteIndex(docType, false)
			fmt.Printf("Error on getIndex:  %s\n", err)
			return err
		}
		if created {
			// Set the couchdb seq to 0 (default) when creating an index (to fetch all changes on UpdateIndex())
			err = instanceIndex.getIndexDocTypeLang(docType, lang).setStoreSeq("0")
			if err != nil {
				fmt.Printf("Error on SetStoreSeq: %s\n", err)
				return err
			}

			version, err := GetMappingVersionFromDescriptionFile(docType)
			if err != nil {
				fmt.Printf("Error on getting mapping version: %s\n", err)
				return err
			}

			// Set the mapping version when creating an index
			err = instanceIndex.getIndexDocTypeLang(docType, lang).setStoreMappingVersion(version)
			if err != nil {
				fmt.Printf("Error on setStoreMappingVersion: %s\n", err)
				return err
			}
		}

	}

	if docType == consts.Files {
		return instanceIndex.initializeIndexDocType(ContentType)
	}

	return nil
}

func (instanceIndex *InstanceIndex) getIndex(docType string, lang string) (*IndexWrapper, bool, error) {
	// call only inside a mutex

	created := false

	// Send fetched index if already exists
	if instanceIndex.indexList[docType][lang] != nil {
		return instanceIndex.indexList[docType][lang], created, nil
	}

	fullIndexPath := instanceIndex.getPathIndex(docType, lang)
	i, errOpen := bleve.Open(fullIndexPath)

	// Create it if it doesn't exist
	if errOpen == bleve.ErrorIndexPathDoesNotExist {
		created = true

		indexMapping := bleve.NewIndexMapping()

		err := AddTypeMapping(indexMapping, docType, lang, instanceIndex.getHighlight())
		if err != nil {
			fmt.Printf("Error on adding type mapping: %s\n", err)
			return nil, created, err
		}
		fmt.Printf("Creating new index %s...\n", fullIndexPath)
		i, err2 := bleve.New(fullIndexPath, indexMapping)
		if err2 != nil {
			fmt.Printf("Error on creating new Index: %s\n", err2)
			return nil, created, err2
		}

		return &IndexWrapper{i}, created, nil

	} else if errOpen != nil {
		fmt.Printf("Error on creating new Index %s: %s\n", fullIndexPath, errOpen)
		return nil, created, errOpen
	}

	created = false

	fmt.Println("found existing Index", instanceIndex.getInstanceName(), docType, lang)
	return &IndexWrapper{i}, created, nil
}

func (instanceIndex *InstanceIndex) getDocTypeList() []string {
	docTypeList := make([]string, len(instanceIndex.indexList))
	i := 0
	for docType := range instanceIndex.indexList {
		docTypeList[i] = docType
		i++
	}
	return docTypeList
}

func (instanceIndex *InstanceIndex) getDocTypeLangList(docType string) []string {
	langList := make([]string, len(instanceIndex.indexList[docType]))
	i := 0
	for lang := range instanceIndex.indexList[docType] {
		langList[i] = lang
		i++
	}
	return langList
}

func (instanceIndex *InstanceIndex) getIndexDocTypeLang(docType string, lang string) *IndexWrapper {
	return instanceIndex.indexList[docType][lang]
}

func (instanceIndex *InstanceIndex) getHighlight() bool {
	return instanceIndex.indexHighlight
}

func (instanceIndex *InstanceIndex) setHighlight(highlight bool) {
	instanceIndex.indexHighlight = highlight
}

func (instanceIndex *InstanceIndex) getContent() bool {
	return instanceIndex.indexContent
}

func (instanceIndex *InstanceIndex) setContent(content bool) {
	instanceIndex.indexContent = content
}

func (instanceIndex *InstanceIndex) lockInstance() {
	instanceIndex.indexMu.Lock()
}

func (instanceIndex *InstanceIndex) unlockInstance() {
	instanceIndex.indexMu.Unlock()
}

func (instanceIndex *InstanceIndex) getInstanceName() string {
	return instanceIndex.instName
}

func (instanceIndex *InstanceIndex) WriteOptionsInstance(options OptionsIndex) error {
	data2, err := yaml.Marshal(options)
	if err != nil {
		fmt.Printf("Error on marshal yaml", err)
		return err
	}

	err = os.MkdirAll(prefixPath+instanceIndex.getInstanceName(), 0777)
	if err != nil {
		fmt.Printf("Error on mkdirall", err)
		return err
	}

	err = ioutil.WriteFile(prefixPath+instanceIndex.getInstanceName()+"/config.yml", data2, 0666)
	if err != nil {
		fmt.Printf("Error on write file: %s\n", err)
		return err
	}

	return nil
}

func (instanceIndex *InstanceIndex) SetOptionsInstance(options map[string]bool) (map[string]bool, error) {

	if content, ok := options["content"]; ok {
		instanceIndex.setContent(content)
	}

	if highlight, ok := options["highlight"]; ok {
		instanceIndex.setHighlight(highlight)
	}

	err := instanceIndex.WriteOptionsInstance(OptionsIndex{instanceIndex.getHighlight(), instanceIndex.getContent()})
	if err != nil {
		return nil, err
	}

	return map[string]bool{
		"content":  instanceIndex.getContent(),
		"higlight": instanceIndex.getHighlight(),
	}, nil
}

func (instanceIndex *InstanceIndex) GetMappingVersion(docType string, lang string) (string, error) {
	return instanceIndex.getIndexDocTypeLang(docType, lang).getStoreMappingVersion()
}

func (instanceIndex *InstanceIndex) ReIndex(docType string) error {

	err := instanceIndex.checkInstanceDocType(docType)
	if err == nil {
		err = instanceIndex.deleteIndex(docType, false)
		if err != nil {
			return err
		}
	}

	// Re-initialize indexes var with docType
	err = instanceIndex.initializeIndexDocType(docType)
	if err != nil {
		return err
	}

	// Update index
	err = AddUpdateIndexJob(UpdateIndexNotif{instanceIndex.getInstanceName(), docType, 0})
	if err != nil {
		return err
	}

	return nil
}

func (instanceIndex *InstanceIndex) ReplicateAll() error {
	for _, docType := range instanceIndex.getDocTypeList() {
		for _, lang := range instanceIndex.getDocTypeLangList(docType) {
			_, err := instanceIndex.Replicate(docType, lang)
			if err != nil {
				fmt.Printf("Error on replication: %s\n", err)
				return err
			}
		}
	}
	return nil
}

func (instanceIndex *InstanceIndex) Replicate(docType string, lang string) (string, error) {

	path := instanceIndex.getPathIndex(docType, lang)

	_, store, err := (*instanceIndex.getIndexDocTypeLang(docType, lang)).Advanced()
	if err != nil {
		fmt.Println(err)
		return "", err
	}
	r, err := store.Reader()
	if err != nil {
		fmt.Println(err)
		return "", err
	}

	tmpFile, err := ioutil.TempFile(path, "store.replicate.")
	if err != nil {
		fmt.Println(err)
		return "", err
	}

	err = r.WriteTo(tmpFile)
	if err != nil {
		fmt.Println(err)
		return "", err
	}
	err = tmpFile.Close()
	if err != nil {
		fmt.Println(err)
		return "", err
	}
	err = r.Close()
	if err != nil {
		fmt.Println(err)
		return "", err
	}

	return tmpFile.Name(), nil
}

func (instanceIndex *InstanceIndex) DeleteAllIndexes(querySide bool) error {

	for _, docType := range instanceIndex.getDocTypeList() {
		if docType == ContentType {
			continue
		}
		err := instanceIndex.deleteIndex(docType, querySide)
		if err != nil {
			return err
		}
	}
	return nil
}

func (instanceIndex *InstanceIndex) deleteIndex(docType string, querySide bool) error {

	for _, lang := range instanceIndex.getDocTypeLangList(docType) {
		if instanceIndex.getIndexDocTypeLang(docType, lang) != nil {
			(*instanceIndex.getIndexDocTypeLang(docType, lang)).Close()
		}
		err := os.RemoveAll(instanceIndex.getPathIndex(docType, lang))
		if err != nil {
			return err
		}
		if querySide {
			err = instanceIndex.notifyDeleteIndexQuery(docType, lang)
			if err != nil {
				fmt.Printf("Error telling query to delete index: %s\n", err)
				return err
			}
		}
	}

	delete(instanceIndex.indexList, docType)

	if docType == consts.Files {
		return instanceIndex.deleteIndex(ContentType, querySide)
	}

	return nil
}

func (instanceIndex *InstanceIndex) notifyDeleteIndexQuery(docType string, lang string) error {

	inst, err := instance.Get(instanceIndex.getInstanceName())
	if err != nil {
		fmt.Printf("Error on getting instance from instance name: %s\n", err)
		return err
	}

	opts := &request.Options{
		Method:  http.MethodPost,
		Scheme:  inst.Scheme(),
		Domain:  inst.DomainName(),
		Path:    "/fulltext/_delete_index_query/" + instanceIndex.getInstanceName() + "/" + docType + "/" + lang,
		Headers: request.Headers{
			// Deal with permissions
		},
		Body: nil,
	}
	_, err = request.Req(opts)
	if err != nil {
		fmt.Println("Error on POST request")
		fmt.Println(err)
		return err
	}

	return nil
}

func (instanceIndex *InstanceIndex) makeSureInstanceDocTypeReady(docType string) error {

	err := instanceIndex.checkInstanceDocType(docType)

	if err != nil {
		err = instanceIndex.initializeIndexDocType(docType)

		if err != nil {
			return err
		}
	}
	return nil
}

func (instanceIndex *InstanceIndex) checkInstanceDocType(docType string) error {
	if _, ok := instanceIndex.indexList[docType]; !ok {
		return errors.New("DocType not found in CheckInstanceDocType")
	}
	return nil
}

func (instanceIndex *InstanceIndex) checkInstanceDocTypeLang(docType string, lang string) error {
	languages := instanceIndex.getDocTypeLangList(docType)

	for _, l := range languages {
		if l == lang {
			return nil
		}
	}
	return errors.New("Language not found in checkInstanceDocTypeLang")
}

func (instanceIndex *InstanceIndex) sendIndexToQuery(docType, lang string) error {

	tmpFileName, err := instanceIndex.Replicate(docType, lang)
	if err != nil {
		fmt.Println("Error on replicate when sending index to query")
		fmt.Println(err)
		return err
	}
	defer os.Remove(tmpFileName)

	body, err := ioutil.ReadFile(tmpFileName)
	if err != nil {
		fmt.Println("Error opening new alias")
		fmt.Println(err)
		return err
	}

	inst, err := instance.Get(instanceIndex.getInstanceName())
	if err != nil {
		fmt.Printf("Error on getting instance from instance name: %s\n", err)
		return err
	}

	opts := &request.Options{
		Method: http.MethodPost,
		Scheme: inst.Scheme(),
		Domain: inst.DomainName(),
		Path:   "/fulltext/_update_index_alias/" + instanceIndex.getInstanceName() + "/" + docType + "/" + lang,
		Headers: request.Headers{
			"Content-Type": "application/indexstore", // See which content-type ?
			// Deal with permissions
		},
		Body: bytes.NewReader(body),
	}
	_, err = request.Req(opts)
	if err != nil {
		fmt.Println("Error on POST request")
		fmt.Println(err)
		return err
	}
	return nil
}

func (instanceIndex *InstanceIndex) getPathIndex(docType, lang string) string {
	return prefixPath + instanceIndex.getInstanceName() + "/" + lang + "/" + docType
}
