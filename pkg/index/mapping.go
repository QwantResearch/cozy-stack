package index

import (
	"github.com/blevesearch/bleve"
	"github.com/blevesearch/bleve/analysis/lang/en"
	"github.com/blevesearch/bleve/analysis/lang/fr"
	"github.com/blevesearch/bleve/mapping"
	"github.com/cozy/cozy-stack/pkg/consts"
	// "github.com/blevesearch/bleve/analysis/analyzer/simple" // Might be useful to check for other Analyzers (maybe make one ourselves)
)

func GetAvailableLanguages() []string {
	return []string{fr.AnalyzerName, en.AnalyzerName}
}

func AddTypeMapping(indexMapping *mapping.IndexMappingImpl, docType string, lang string) {

	// For each type of document, don't forget to Add Document Disable Mapping on useless fields
	// It affects performances a lot

	switch docType {
	case consts.PhotosAlbums:
		indexMapping = AddPhotoAlbumMapping(indexMapping, lang)
		break
	case consts.Files:
		indexMapping = AddFileMapping(indexMapping, lang)
		break
	case "io.cozy.bank.accounts":
		indexMapping = AddBankAccountMapping(indexMapping, lang)
		break
	}
	indexMapping.TypeField = "docType"
}

func AddPhotoAlbumMapping(indexMapping *mapping.IndexMappingImpl, lang string) *mapping.IndexMappingImpl {
	photosAlbumMapping := bleve.NewDocumentMapping()

	englishTextFieldMapping := bleve.NewTextFieldMapping()
	englishTextFieldMapping.Analyzer = lang
	// englishTextFieldMapping.IncludeInAll = true

	photosAlbumMapping.AddFieldMappingsAt("name", englishTextFieldMapping)

	indexMapping.AddDocumentMapping(consts.PhotosAlbums, photosAlbumMapping)

	return indexMapping
}

func AddFileMapping(indexMapping *mapping.IndexMappingImpl, lang string) *mapping.IndexMappingImpl {
	fileMapping := bleve.NewDocumentMapping()

	// Type fields mapping
	englishTextFieldMapping := bleve.NewTextFieldMapping()
	englishTextFieldMapping.Analyzer = lang
	englishTextFieldMapping.IncludeInAll = true

	fileMapping.AddFieldMappingsAt("name", englishTextFieldMapping)
	fileMapping.AddFieldMappingsAt("tags", englishTextFieldMapping)

	dateMapping := bleve.NewDateTimeFieldMapping()

	fileMapping.AddFieldMappingsAt("created_at", dateMapping)
	fileMapping.AddFieldMappingsAt("updated_at", dateMapping)
	// TODO: check tag mapping (knowing it's an array)

	// store field only
	storeFieldMapping := bleve.NewTextFieldMapping()
	storeFieldMapping.Index = false
	storeFieldMapping.Store = true
	fileMapping.AddFieldMappingsAt("_rev", storeFieldMapping)

	// Ignore fields mapping
	ignoreMapping := bleve.NewDocumentDisabledMapping()
	fileMapping.AddSubDocumentMapping("metadata", ignoreMapping)
	fileMapping.AddSubDocumentMapping("referenced_by", ignoreMapping)
	fileMapping.AddSubDocumentMapping("_id", ignoreMapping)
	fileMapping.AddSubDocumentMapping("class", ignoreMapping)
	fileMapping.AddSubDocumentMapping("executable", ignoreMapping)
	fileMapping.AddSubDocumentMapping("mime", ignoreMapping)
	fileMapping.AddSubDocumentMapping("trashed", ignoreMapping)
	fileMapping.AddSubDocumentMapping("type", ignoreMapping)
	fileMapping.AddSubDocumentMapping("dir_id", ignoreMapping)
	fileMapping.AddSubDocumentMapping("size", ignoreMapping)
	fileMapping.AddSubDocumentMapping("md5sum", ignoreMapping)

	indexMapping.AddDocumentMapping(consts.Files, fileMapping)

	return indexMapping
}

func AddBankAccountMapping(indexMapping *mapping.IndexMappingImpl, lang string) *mapping.IndexMappingImpl {
	BankAccountMapping := bleve.NewDocumentMapping()

	englishTextFieldMapping := bleve.NewTextFieldMapping()
	englishTextFieldMapping.Analyzer = lang
	englishTextFieldMapping.IncludeInAll = true

	simpleMapping := bleve.NewTextFieldMapping()
	// Todo: check it is actually without analyzer

	numberMapping := bleve.NewNumericFieldMapping()

	BankAccountMapping.AddFieldMappingsAt("label", englishTextFieldMapping)
	BankAccountMapping.AddFieldMappingsAt("institutionLabel", englishTextFieldMapping)
	BankAccountMapping.AddFieldMappingsAt("balance", numberMapping)
	BankAccountMapping.AddFieldMappingsAt("type", englishTextFieldMapping)
	BankAccountMapping.AddFieldMappingsAt("number", simpleMapping)
	BankAccountMapping.AddFieldMappingsAt("iban", simpleMapping)
	BankAccountMapping.AddFieldMappingsAt("serviceID", numberMapping) // Todo : test when is undefined

	indexMapping.AddDocumentMapping("io.cozy.bank.accounts", BankAccountMapping)

	return indexMapping
}

// Todo: io.cozy.bank.operations
