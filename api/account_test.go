package api_test

import (
	"encoding/json"
	"fmt"
	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/vechain/thor/api"
	"github.com/vechain/thor/lvldb"
	"github.com/vechain/thor/state"
	"github.com/vechain/thor/thor"
	"io/ioutil"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
)

const (
	emptyRootHash = "56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421"
	testAddress   = "56e81f171bcc55a6ff8345e692c0f86e5b48e01a"
)

func TestAccount(t *testing.T) {
	db, _ := lvldb.NewMem()
	hash, _ := thor.ParseHash(emptyRootHash)
	s, _ := state.New(hash, db)
	ai := api.NewAccountInterface(s)
	router := mux.NewRouter()
	api.NewAccountHTTPRouter(router, ai)
	ts := httptest.NewServer(router)
	defer ts.Close()
	address, _ := thor.ParseAddress(testAddress)
	storageKey := thor.BytesToHash([]byte("key"))
	type account struct {
		balance *big.Int
		code    []byte
		storage thor.Hash
	}

	accounts := []struct {
		in, want account
	}{
		{
			account{big.NewInt(10), []byte{0x11, 0x12}, thor.BytesToHash([]byte("v1"))},
			account{big.NewInt(10), []byte{0x11, 0x12}, thor.BytesToHash([]byte("v1"))},
		},
		{
			account{big.NewInt(100), []byte{0x14, 0x15}, thor.BytesToHash([]byte("v2"))},
			account{big.NewInt(100), []byte{0x14, 0x15}, thor.BytesToHash([]byte("v2"))},
		},
		{
			account{big.NewInt(1000), []byte{0x20, 0x21}, thor.BytesToHash([]byte("v2"))},
			account{big.NewInt(1000), []byte{0x20, 0x21}, thor.BytesToHash([]byte("v2"))},
		},
	}

	for _, v := range accounts {
		s.SetBalance(address, v.in.balance)
		s.SetCode(address, v.in.code)
		s.SetStorage(address, storageKey, v.in.storage)
		s.Stage().Commit()

		res, err := http.Get(ts.URL + fmt.Sprintf("/account/address/%v/balance", address.String()))
		if err != nil {
			t.Fatal(err)
		}
		r, err := ioutil.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}

		b := make(map[string]*big.Int)
		if err := json.Unmarshal(r, &b); err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, v.want.balance, b["balance"], "balance should be equal")

		res, err = http.Get(ts.URL + fmt.Sprintf("/account/address/%v/code", address.String()))
		if err != nil {
			t.Fatal(err)
		}
		r, err = ioutil.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		c := make(map[string][]byte)
		if err := json.Unmarshal(r, &c); err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, v.want.code, c["code"], "code should be equal")

		res, err = http.Get(ts.URL + fmt.Sprintf("/account/address/%v/key/%v/storage", address.String(), storageKey.String()))
		if err != nil {
			t.Fatal(err)
		}
		r, err = ioutil.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}

		value := make(map[string]string)
		if err := json.Unmarshal(r, &value); err != nil {
			t.Fatal(err)
		}
		h, err := thor.ParseHash(value[storageKey.String()])
		if err != nil {
			t.Fatal(err)
		}
		assert.Equal(t, v.want.storage, h, "storage should be equal")

	}

}