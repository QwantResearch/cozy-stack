package index

// #cgo LDFLAGS: -L${SRCDIR} -lfasttextgo -lstdc++ -lm
// #include <stdlib.h>
// void load_model(char *path);
// void *fasttext_new();
// void fasttext_delete(void *ft);
// void fasttext_load_model(void *ft, char *path);
// int fasttext_predict(void *ft, char *query, float *prob, char **buf, int *size);
// int fasttext_predict_k(void *ft, char *query, int k, float *prob, char **buf, int *sizes);
import "C"
import (
	"errors"
	"fmt"
	"unsafe"
)

func (f *FastText) GetLanguage(sentence string) (string, error) {
	if f.ft == nil {
		return "", errors.New("Predict called on closed FastText")
	}
	preds, err := f.PredictK(sentence, 176)
	if err != nil {
		fmt.Println(err)
		return "", err
	}

	for _, lang := range preds {
		if (lang.Label == "__label__en") || (lang.Label == "__label__fr") {
			return lang.Label[9:], nil
		}
	}

	fmt.Println("Error: language not found, en by default")
	return "en", nil
}

// LoadModel - load FastText model
func LoadModel(path string) {
	C.load_model(C.CString(path))
}

// Predict - predict
func Predict(sentence string) (prob float32, label string, err error) {
	var ft *FastText
	fmt.Println(ft)
	return ft.predict(sentence)
}

// Predict - predict
func (f *FastText) Predict(sentence string) (prob float32, label string, err error) {
	if f.ft == nil {
		return 0, "", errors.New("Predict called on closed FastText")
	}
	return f.predict(sentence)
}

func (f *FastText) predict(sentence string) (prob float32, label string, err error) {
	var cprob C.float
	var buf *C.char
	var size C.int
	var ft unsafe.Pointer

	if f != nil {
		ft = f.ft
	}
	if sentence != "" && sentence[len(sentence)-1] != '\n' {
		sentence += "\n"
	}
	cs := C.CString(sentence)
	fmt.Println(ft)
	ret := C.fasttext_predict(ft, cs, &cprob, &buf, &size)
	C.free(unsafe.Pointer(cs))

	if ret != 0 {
		err = errors.New("error in prediction")
	} else {
		label = C.GoStringN(buf, size)
		prob = float32(cprob)
		C.free(unsafe.Pointer(buf))
	}

	return prob, label, err
}

// Prediction is used in a result of PredictK
type Prediction struct {
	Prob  float32
	Label string
}

// PredictK returns K top predictions
func PredictK(sentence string, k int) ([]Prediction, error) {
	var ft *FastText
	return ft.predictK(sentence, k)
}

// PredictK returns K top predictions
func (f *FastText) PredictK(sentence string, k int) ([]Prediction, error) {
	if f.ft == nil {
		return nil, errors.New("PredictK called on closed FastText")
	}
	return f.predictK(sentence, k)
}

// predictK returns K top predictions
func (f *FastText) predictK(sentence string, k int) ([]Prediction, error) {
	var cprob *C.float
	cprob = (*C.float)(C.calloc(C.size_t(k), C.sizeof_float))
	var csizes *C.int
	csizes = (*C.int)(C.calloc(C.size_t(k), C.sizeof_int))
	var buf *C.char
	var ft unsafe.Pointer

	if f != nil {
		ft = f.ft
	}
	if sentence != "" && sentence[len(sentence)-1] != '\n' {
		sentence += "\n"
	}
	cs := C.CString(sentence)
	ret := C.fasttext_predict_k(ft, cs, C.int(k), cprob, &buf, csizes)
	C.free(unsafe.Pointer(cs))

	if ret == -1 {
		C.free(unsafe.Pointer(cprob))
		C.free(unsafe.Pointer(csizes))
		return nil, errors.New("error in prediction")
	}

	ps := make([]Prediction, 0, ret)
	pos := 0
	for i := 0; i < int(ret); i++ {
		f := float32(*(*C.float)(unsafe.Pointer(uintptr(unsafe.Pointer(cprob)) + uintptr(i*C.sizeof_float))))
		size := *(*C.int)(unsafe.Pointer(uintptr(unsafe.Pointer(csizes)) + uintptr(i*C.sizeof_int)))
		s := C.GoStringN((*C.char)(unsafe.Pointer(uintptr(unsafe.Pointer(buf))+uintptr(pos))), C.int(size))
		ps = append(ps, Prediction{f, s})
		pos += int(size)
	}
	C.free(unsafe.Pointer(cprob))
	C.free(unsafe.Pointer(csizes))
	C.free(unsafe.Pointer(buf))

	return ps, nil
}

// FastText represent instance of fasttext classifier
type FastText struct {
	ft unsafe.Pointer
}

// New returns new instance of fasttext classifier
func NewFastTextInst() *FastText {
	return &FastText{C.fasttext_new()}
}

// Close deletes the underlying fastext classifier. It is safe to call
// Close multiple times, but calling other method on the closed
// FastText instance will panic.
func (f *FastText) Close() {
	if f.ft != nil {
		C.fasttext_delete(f.ft)
		f.ft = nil
	}
}

// LoadModel - load FastText model
func (f *FastText) LoadModel(path string) error {
	if f.ft == nil {
		return errors.New("LoadModel called on closed FastText")
	}
	cPath := C.CString(path)
	C.fasttext_load_model(f.ft, cPath)
	C.free(unsafe.Pointer(cPath))
	return nil
}
