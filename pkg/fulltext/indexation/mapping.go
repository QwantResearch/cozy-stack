package indexation

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"

	"github.com/blevesearch/bleve"
	"github.com/blevesearch/bleve/analysis/analyzer/keyword"
	"github.com/blevesearch/bleve/analysis/lang/en"
	"github.com/blevesearch/bleve/analysis/lang/fr"
	"github.com/blevesearch/bleve/mapping"
	// "github.com/blevesearch/bleve/analysis/analyzer/simple" // Might be useful to check for other Analyzers (maybe make one ourselves)
)

const (
	MappingDescriptionPath = "bleve/mapping_description.json"
)

func GetAvailableLanguages() []string {
	return []string{fr.AnalyzerName, en.AnalyzerName}

	//TODO: store language in mapping description? (per doctype?)
}

func AddTypeMapping(indexMapping *mapping.IndexMappingImpl, docType string, lang string) error {

	mappingDescription, err := GetDocTypeMappingFromDescriptionFile(docType)
	if err != nil {
		fmt.Printf("Error on getting mapping description: %s\n", err)
		return err
	}

	documentMapping := bleve.NewDocumentMapping()

	// We set dynamic to false to ignore all fields by default.
	// See: https://groups.google.com/forum/#!searchin/bleve/dynamic|sort:date/bleve/XeztWQOlT7o/BZ4WvhqhBwAJ
	documentMapping.Dynamic = false

	AddFieldMappingsFromDescription(documentMapping, mappingDescription, lang)

	// Add docType field mapping, as keyword, common to all doctypes
	keywordFieldMapping, err := CreateFieldMapping("keywordField", lang)
	if err != nil {
		fmt.Printf("Error when creating keywordFielMapping for docType: %s\n", err)
		return err
	}
	documentMapping.AddFieldMappingsAt("docType", keywordFieldMapping)

	indexMapping.AddDocumentMapping(docType, documentMapping)
	indexMapping.TypeField = "docType"

	return nil
}

func AddFieldMappingsFromDescription(documentMapping *mapping.DocumentMapping, mappingDescription map[string]interface{}, lang string) error {

	// mappingDescription must be either string or map[string]interface{}
	// In the first case we create the corresponding mapping field and add it to the documentMapping
	// In the second case we create a subdocument and call this function recursivelyon it

	for fieldName, fieldMapping := range mappingDescription {
		fieldMappingString, ok := fieldMapping.(string)
		if !ok {
			fieldMappingMap, ok := fieldMapping.(map[string]interface{})
			if !ok {
				err := errors.New("The field '" + fieldName + "' from the description file is neither string nor nested map[string]interface{}.")
				fmt.Printf("Error on parsing mapping description json file: %s\n", err)
				return err
			}
			// Nested structure: call this function recursively
			subDocumentMapping := bleve.NewDocumentMapping()
			err := AddFieldMappingsFromDescription(subDocumentMapping, fieldMappingMap, lang)
			if err != nil {
				return err
			}
			documentMapping.AddSubDocumentMapping(fieldName, subDocumentMapping)
		} else {
			newFieldMapping, err := CreateFieldMapping(fieldMappingString, lang)
			if err != nil {
				fmt.Printf("Error on creating mapping: %s\n", err)
				return err
			}
			documentMapping.AddFieldMappingsAt(fieldName, newFieldMapping)
		}
	}

	return nil
}

func GetDocTypeMappingFromDescriptionFile(docType string) (map[string]interface{}, error) {
	var mapping map[string]map[string]interface{}

	mappingDescriptionFile, err := ioutil.ReadFile(MappingDescriptionPath)
	if err != nil {
		fmt.Printf("Error on getting description file: %s\n", err)
		return nil, err
	}

	err = json.Unmarshal(mappingDescriptionFile, &mapping)
	if err != nil {
		fmt.Printf("Error on unmarshalling: %s\n", err)
		return nil, err
	}

	return mapping[docType], nil
}

func CreateFieldMapping(mappingType string, lang string) (*mapping.FieldMapping, error) {
	switch mappingType {
	case "textField":
		textFieldMapping := bleve.NewTextFieldMapping()
		textFieldMapping.Analyzer = lang
		textFieldMapping.IncludeInAll = true
		return textFieldMapping, nil
	case "keywordField":
		keywordFieldMapping := bleve.NewTextFieldMapping()
		keywordFieldMapping.Analyzer = keyword.Name
		return keywordFieldMapping, nil
	case "numberField":
		numberMapping := bleve.NewNumericFieldMapping()
		return numberMapping, nil
	case "dateField":
		dateMapping := bleve.NewDateTimeFieldMapping()
		return dateMapping, nil
	case "storeField":
		storeFieldMapping := bleve.NewTextFieldMapping()
		storeFieldMapping.Index = false
		storeFieldMapping.Store = true
		return storeFieldMapping, nil
	}

	// nothing matched, we return an error
	return nil, errors.New("The Mapping Field " + mappingType + " doesn't correspond to any of the known mapping fields.")
}
