// The Licensed Work is (c) 2022 Sygma
// SPDX-License-Identifier: BUSL-1.1

package keyshare

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"sync"

	"github.com/binance-chain/tss-lib/ecdsa/keygen"
	"github.com/libp2p/go-libp2p/core/peer"
)

// Keyshare stores key received from keygen or resharing
// and treshold and peers from current signing committee
type Keyshare struct {
	Key       keygen.LocalPartySaveData
	Threshold int
	Peers     []peer.ID
}

func NewKeyshare(key keygen.LocalPartySaveData, threshold int, peers []peer.ID) Keyshare {
	return Keyshare{
		Key:       key,
		Threshold: threshold,
		Peers:     peers,
	}
}

type KeyshareStore struct {
	mu   sync.Mutex
	path string
}

func NewKeyshareStore(filePath string) *KeyshareStore {
	return &KeyshareStore{
		path: filePath,
	}
}

// LockKeyshare locks keyshare from reading and writing to
// prevent keygen or resharing being done in parallel with other
// tss processes.
func (ks *KeyshareStore) LockKeyshare() {
	ks.mu.Lock()
}

// UnlockKeyshare unlocks keyshare to allow for tss processes to continue
func (ks *KeyshareStore) UnlockKeyshare() {
	ks.mu.Unlock()
}

// StoreKeyshare stores keyshare generated by keygen or reshare into file and truncates
// old keyshare.
func (ks *KeyshareStore) StoreKeyshare(keyshare Keyshare) error {
	f, err := os.OpenFile(ks.path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer f.Close()

	kb, err := json.Marshal(&keyshare)
	if err != nil {
		return err
	}

	_, err = f.Write(kb)
	return err
}

// GetKeyshare fetches current keyshare from file.
// Can be a blocking call if keygen or resharing are pending.
func (ks *KeyshareStore) GetKeyshare() (Keyshare, error) {
	k := Keyshare{}

	kb, err := ioutil.ReadFile(ks.path)
	if err != nil {
		return k, fmt.Errorf("error on reading keyshare file: %s", err)
	}

	err = json.Unmarshal(kb, &k)
	if err != nil {
		return k, fmt.Errorf("error on unmarshaling keyshare file: %s", err)
	}

	return k, err
}
