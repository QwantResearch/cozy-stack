package indexation

import (
	"github.com/blevesearch/bleve"
)

type IndexWrapper struct {
	bleve.Index
}

func (index *IndexWrapper) setStoreSeq(rev string) error {
	// Call only inside a mutex lock
	return (*index).SetInternal([]byte("seq"), []byte(rev))
}

func (index *IndexWrapper) getStoreSeq() (string, error) {
	// Call only inside a mutex lock
	res, err := (*index).GetInternal([]byte("seq"))
	return string(res), err
}

func (index *IndexWrapper) setStoreMd5sum(fileId string, md5sum string) error {
	// Call only inside a mutex lock
	return (*index).SetInternal([]byte("md5sum"+fileId), []byte(md5sum))
}

func (index *IndexWrapper) getStoreMd5sum(fileId string) (string, error) {
	// Call only inside a mutex lock
	res, err := (*index).GetInternal([]byte("md5sum" + fileId))
	return string(res), err
}

func (index *IndexWrapper) setStoreMappingVersion(version string) error {
	// Call only inside a mutex lock
	return (*index).SetInternal([]byte("mappingVersion"), []byte(version))
}

func (index *IndexWrapper) getStoreMappingVersion() (string, error) {
	// Call only inside a mutex lock
	res, err := (*index).GetInternal([]byte("mappingVersion"))
	return string(res), err
}
