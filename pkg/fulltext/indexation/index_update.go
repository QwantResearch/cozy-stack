package indexation

import (
	"fmt"

	"github.com/blevesearch/bleve"
	"github.com/cozy/cozy-stack/pkg/consts"
	"github.com/cozy/cozy-stack/pkg/couchdb"
	"github.com/cozy/cozy-stack/pkg/instance"
)

var batchSize = 300

type IndexUpdater struct {
	instanceIndex     *InstanceIndex
	docType           string
	content           bool
	batchIndex        map[string]*BatchIndex
	batchIndexContent map[string]*BatchIndex
}

type BatchIndex struct {
	batch *bleve.Batch
	index *IndexWrapper
	count int
}

func (indexUpdater *IndexUpdater) init(instanceIndex *InstanceIndex, docType string) {

	indexUpdater.instanceIndex = instanceIndex
	indexUpdater.docType = docType
	indexUpdater.content = instanceIndex.indexContent && docType == consts.Files

	indexUpdater.batchIndex = make(map[string]*BatchIndex)

	for _, lang := range indexController.GetLanguages() {
		indexUpdater.batchIndex[lang] = &BatchIndex{}
		indexUpdater.batchIndex[lang].index = indexUpdater.instanceIndex.getIndexDocTypeLang(docType, lang)
		indexUpdater.batchIndex[lang].batch = (indexUpdater.batchIndex[lang].index).NewBatch()
		indexUpdater.batchIndex[lang].count = 0
	}

	if indexUpdater.content {
		indexUpdater.batchIndexContent = make(map[string]*BatchIndex)

		for _, lang := range indexController.GetLanguages() {
			indexUpdater.batchIndexContent[lang] = &BatchIndex{}
			indexUpdater.batchIndexContent[lang].index = indexUpdater.instanceIndex.getIndexDocTypeLang(ContentType, lang)
			indexUpdater.batchIndexContent[lang].batch = (indexUpdater.batchIndexContent[lang].index).NewBatch()
			indexUpdater.batchIndexContent[lang].count = 0
		}
	}
}

func (indexUpdater *IndexUpdater) UpdateIndex() error {
	response, err := indexUpdater.getResults()
	if err != nil {
		return err
	}

	for _, result := range response.Results {

		if typ, ok := result.Doc.M["type"]; !ok || typ.(string) == "directory" {
			// This is a directory, we ignore this doc
			continue
		}

		originalIndexLang := indexUpdater.findWhichLangIndexDoc(result.DocID)

		// Delete the files that are trashed = true or _deleted = true
		if result.Doc.Get("_deleted") == true || result.Doc.Get("trashed") == true {

			err := indexUpdater.DeleteDoc(originalIndexLang, result.DocID)
			if err != nil {
				fmt.Printf("Error on DeleteDoc: %s\n", err)
				return err
			}
			continue
		}

		if originalIndexLang != "" {

			// We found the document so we should update it in the original index

			err := indexUpdater.UpdateDoc(originalIndexLang, result)
			if err != nil {
				fmt.Printf("Error on UpdateDoc: %s\n", err)
				return err
			}

		} else {
			// We couldn't find the document, so we should create it in the predicted language index

			err := indexUpdater.CreateDoc(result)
			if err != nil {
				fmt.Printf("Error on CreateDoc: %s\n", err)
				return err
			}
		}

	}

	for _, lang := range indexController.GetLanguages() {

		indexUpdater.batchIndex[lang].Close()

		if indexUpdater.content {
			indexUpdater.batchIndexContent[lang].Close()
		}

		// Store the new seq number in the indexes
		err = indexUpdater.batchIndex[lang].index.setStoreSeq(response.LastSeq)
		if err != nil {
			fmt.Printf("Error on SetStoreSeq: %s\n", err)
			return err
		}
	}

	return nil
}

func (indexUpdater *IndexUpdater) getResults() (*couchdb.ChangesResponse, error) {

	// Set request to get last changes
	last_store_seq, err := indexUpdater.batchIndex[indexController.GetLanguages()[0]].index.getStoreSeq()
	if err != nil {
		fmt.Printf("Error on GetStoredSeq: %s\n", err)
		return nil, err
	}

	var request = &couchdb.ChangesRequest{
		DocType:     indexUpdater.docType,
		Since:       last_store_seq, // Set with last seq
		IncludeDocs: true,
	}

	inst, err := instance.Get(indexUpdater.instanceIndex.getInstanceName())
	if err != nil {
		fmt.Printf("Error on getting instance from instance name: %s\n", err)
		return nil, err
	}

	// Fetch last changes
	// TODO : check how getchanges behave when there are multiple changes for a doc since last seq
	response, err := couchdb.GetChanges(inst, request)
	if err != nil {
		fmt.Printf("Error on getChanges: %s\n", err)
		return nil, err
	}

	return response, nil
}

func (indexUpdater *IndexUpdater) CreateDoc(result couchdb.Change) error {
	var pred string
	var err error

	// If doc is a file and indexContent is true, we need to index content
	if indexUpdater.content {
		content, err := getContentFile(indexUpdater.instanceIndex.getInstanceName(), result.DocID)
		if err != nil {
			fmt.Printf("Error on getContentFile", err)
			return err
		}

		pred, err = ft_language.GetLanguage(content)
		if err != nil {
			fmt.Printf("Error on language prediction:  %s\n", err)
			return err
		}

		err = indexUpdater.batchIndexContent[pred].Index(result.DocID, map[string]interface{}{"content": content, "docType": ContentType})
		if err != nil {
			fmt.Printf("Error on UpdateContent: %s\n", err)
			return err
		}

		err = indexUpdater.batchIndex[pred].index.setStoreMd5sum(result.DocID, result.Doc.M["md5sum"].(string))
		if err != nil {
			fmt.Printf("Error on setStoreMd5sum: %s\n", err)
			return err
		}
	} else {
		pred, err = ft_language.GetLanguage(result.Doc.M["name"].(string))
		if err != nil {
			fmt.Printf("Error on language prediction:  %s\n", err)
			return err
		}
	}

	result.Doc.M["docType"] = indexUpdater.docType

	return indexUpdater.batchIndex[pred].Index(result.DocID, result.Doc.M)
}

func (indexUpdater *IndexUpdater) UpdateDoc(originalIndexLang string, result couchdb.Change) error {
	result.Doc.M["docType"] = indexUpdater.docType

	// If doc is a file and indexContent is true, we need to check if content has been modified since last indexation
	if indexUpdater.content {
		md5sum, err := indexUpdater.batchIndex[originalIndexLang].index.getStoreMd5sum(result.DocID)
		if err != nil {
			fmt.Printf("Error on getStoreMd5sum: %s\n", err)
			return err
		}
		if md5sum != result.Doc.M["md5sum"] {
			// Get new content and index it
			content, err := getContentFile(indexUpdater.instanceIndex.getInstanceName(), result.DocID)
			if err != nil {
				fmt.Printf("Error on getContentFile: %s\n", err)
				return err
			}
			err = indexUpdater.batchIndexContent[originalIndexLang].Index(result.DocID, map[string]interface{}{"content": content, "docType": ContentType})
			if err != nil {
				fmt.Printf("Error on UpdateContent: %s\n", err)
				return err
			}

			err = indexUpdater.batchIndex[originalIndexLang].index.setStoreMd5sum(result.DocID, result.Doc.M["md5sum"].(string))
			if err != nil {
				fmt.Printf("Error on setStoreMd5sum: %s\n", err)
				return err
			}
		}
	}

	return indexUpdater.batchIndex[originalIndexLang].Index(result.DocID, result.Doc.M)
}

func (indexUpdater *IndexUpdater) DeleteDoc(originalIndexLang string, DocID string) error {
	if originalIndexLang != "" {
		err := indexUpdater.batchIndex[originalIndexLang].Delete(DocID)
		if err != nil {
			return err
		}
		if indexUpdater.content {
			return indexUpdater.batchIndexContent[originalIndexLang].Delete(DocID)
		}
	}
	// else the file has already been deleted or hadn't had been indexed before either
	return nil
}

func (indexUpdater *IndexUpdater) findWhichLangIndexDoc(id string) string {

	for lang := range indexUpdater.batchIndex {

		doc, err := (*indexUpdater.batchIndex[lang].index).Document(id)
		if err != nil {
			fmt.Printf("%s", err)
		}
		if doc != nil {
			return lang
		}

	}
	return ""
}

func (batchIndex *BatchIndex) Index(DocID string, result interface{}) error {
	err := batchIndex.batch.Index(DocID, result)
	if err != nil {
		fmt.Println(err)
		return err
	}
	batchIndex.count++
	if batchIndex.count >= batchSize {
		batchIndex.index.Batch(batchIndex.batch)
		batchIndex.count = 0
	}

	return nil
}

func (batchIndex *BatchIndex) Close() {
	if batchIndex.count > 0 {
		batchIndex.index.Batch(batchIndex.batch)
	}
}

func (batchIndex *BatchIndex) Delete(DocID string) error {
	return (*batchIndex.index).Delete(DocID)
}
