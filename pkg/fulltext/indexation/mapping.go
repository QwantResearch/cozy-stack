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
	MappingDescriptionPath = "bleve/mapping_description/"
)

func GetAvailableLanguages() []string {
	return []string{fr.AnalyzerName, en.AnalyzerName}

	//TODO: store language in mapping description? (per doctype?)
}

func AddTypeMapping(indexMapping *mapping.IndexMappingImpl, docType string, lang string, highlight bool) error {

	mappingDescription, err := GetDocTypeMappingFromDescriptionFile(docType)
	if err != nil {
		fmt.Printf("Error on getting mapping description: %s\n", err)
		return err
	}

	documentMapping := bleve.NewDocumentMapping()

	// We set dynamic to false to ignore all fields by default.
	// See: https://groups.google.com/forum/#!searchin/bleve/dynamic|sort:date/bleve/XeztWQOlT7o/BZ4WvhqhBwAJ
	documentMapping.Dynamic = false

	AddFieldMappingsFromDescription(documentMapping, mappingDescription, lang, highlight)

	// Add docType field mapping, as keyword, common to all doctypes
	storeFieldMapping, err := CreateFieldMapping("storeField", lang, false)
	if err != nil {
		fmt.Printf("Error when creating storeFieldMapping for docType: %s\n", err)
		return err
	}
	documentMapping.AddFieldMappingsAt("docType", storeFieldMapping)

	indexMapping.AddDocumentMapping(docType, documentMapping)
	indexMapping.TypeField = "docType"

	return nil
}

func AddFieldMappingsFromDescription(documentMapping *mapping.DocumentMapping, mappingDescription map[string]interface{}, lang string, highlight bool) error {

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
			err := AddFieldMappingsFromDescription(subDocumentMapping, fieldMappingMap, lang, highlight)
			if err != nil {
				return err
			}
			documentMapping.AddSubDocumentMapping(fieldName, subDocumentMapping)
		} else {
			newFieldMapping, err := CreateFieldMapping(fieldMappingString, lang, highlight)
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
	var mapping map[string]interface{}

	mappingDescriptionFile, err := ioutil.ReadFile(MappingDescriptionPath + docType + ".json")
	if err != nil {
		fmt.Printf("Error on getting description file: %s\n", err)
		return nil, err
	}

	err = json.Unmarshal(mappingDescriptionFile, &mapping)
	if err != nil {
		fmt.Printf("Error on unmarshalling: %s\n", err)
		return nil, err
	}

	return mapping, nil
}

func CreateFieldMapping(mappingType string, lang string, highlight bool) (*mapping.FieldMapping, error) {
	switch mappingType {
	case "textField":
		textFieldMapping := bleve.NewTextFieldMapping()
		textFieldMapping.Analyzer = lang
		textFieldMapping.IncludeInAll = true
		if !highlight {
			textFieldMapping.Store = false
		}
		return textFieldMapping, nil
	case "keywordField":
		keywordFieldMapping := bleve.NewTextFieldMapping()
		keywordFieldMapping.Analyzer = keyword.Name
		if !highlight {
			keywordFieldMapping.Store = false
		}
		return keywordFieldMapping, nil
	case "numberField":
		numberMapping := bleve.NewNumericFieldMapping()
		if !highlight {
			numberMapping.Store = false
		}
		return numberMapping, nil
	case "dateField":
		dateMapping := bleve.NewDateTimeFieldMapping()
		if !highlight {
			dateMapping.Store = false
		}
		return dateMapping, nil
	case "storeField":
		storeFieldMapping := bleve.NewTextFieldMapping()
		storeFieldMapping.Index = false
		storeFieldMapping.Store = true
		return storeFieldMapping, nil
	case "timestampField":
		timestampFieldMapping := bleve.NewDateTimeFieldMapping()
		if !highlight {
			timestampFieldMapping.Store = false
		}
		return timestampFieldMapping, nil
	}

	// nothing matched, we return an error
	return nil, errors.New("The Mapping Field " + mappingType + " doesn't correspond to any of the known mapping fields.")
}
