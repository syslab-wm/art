package main

import (
	"crypto/ecdh"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"

	"art/internal/cryptutl"
	"art/internal/keyutl"
	"art/internal/mu"
	"art/internal/proto"
	"art/internal/tree"
)

const shortUsage = `Usage: process_setup_message [options] INDEX PRIV_EK_FILE \ 
	INITIATOR_PUB_IK_FILE SETUP_MSG_FILE`
const usage = `Usage: process_setup_message [options] INDEX PRIV_EK_FILE \
	INITIATOR_PUB_IK_FILE SETUP_MSG_FILE

Process a group setup message as a group member at position INDEX

positional arguments:
  INDEX
	The index position of the 'current' group member that is processing the setup
	message, this index is based off the member's position in the group config
	file, where the first entry is at index 1.

  PRIV_EK_FILE
	The 'current' group member's private ephemeral key file (also called a prekey).  
	This is a PEM-encoded X25519 private key.

  INITIATOR_PUB_IK_FILE
    The initiator's public identity key.  This is a PEM-encoded ED25519 key.

  SETUP_MSG_FILE
	The file containing the group setup message.

options:
  -h, -help
    Show this usage statement and exit.

  -sig-file SETUP_MSG_SIG_FILE
    The setup message's corresponding signature file (signed with the initiator's IK).
    If not provided, the tool will look for a file SETUP_MSG_FILE.sig.

  -out-state STATE_FILE
    The file to output the node's state after processing the setup message. If
    not provided, the default is state.json. 


examples:
  ./process_setup_message -out-state bob-state.json 2 bob-ek.pem \
		alice-ik-pub.pem setup.msg`

func printUsage() {
	fmt.Println(usage)
}

type options struct {
	// positional arguments
	index              int
	privEKFile         string
	initiatorPubIKFile string
	setupMessageFile   string

	// options
	sigFile       string
	treeStateFile string
}

func verifyMessage(publicKeyPath, msgFile, sigFile string) {
	valid, err := cryptutl.VerifySignature(publicKeyPath, msgFile, sigFile)
	if err != nil {
		mu.Die("error: %v", err)
	}
	if !valid {
		mu.Die("error: message signature verification failed for %v", msgFile)
	}
}

func decodeMessage(file *os.File, m *proto.Message) {
	dec := json.NewDecoder(file)
	err := dec.Decode(&m)
	if err != nil {
		mu.Die("error decoding message from file:", err)
	}
}

func readMessage(msgFilePath string, m *proto.Message) {
	msgFile, err := os.Open(msgFilePath)
	if err != nil {
		mu.Die("error opening message file:", err)
	}
	defer msgFile.Close()
	decodeMessage(msgFile, m)
}

func getSetupKey(m *proto.Message) *ecdh.PublicKey {
	suk, err := keyutl.UnmarshalPublicEKFromPEM(m.Suk)
	if err != nil {
		mu.Die("failed to unmarshal public SUK")
	}
	return suk
}

func getPublicTree(m *proto.Message) *tree.PublicNode {
	tree, err := tree.UnmarshalKeysToPublicTree(m.TreeKeys)
	if err != nil {
		mu.Die("error unmarshalling the public tree keys: %v", err)
	}
	return tree
}

// derive the member's private leaf key
func deriveLeafKey(privKeyFile string, setupKey *ecdh.PublicKey) *ecdh.PrivateKey {
	leafKey, err := tree.DeriveLeafKey(privKeyFile, setupKey)
	if err != nil {
		mu.Die("error deriving the private leaf key: %v", err)
	}
	return leafKey
}

func deriveTreeKey(state *tree.TreeState, index int) *ecdh.PrivateKey {
	// find the nodes on the copath
	copathNodes := make([]*ecdh.PublicKey, 0)
	copathNodes = tree.CoPath(state.PublicTree, index, copathNodes)

	// with the leaf key, derive the private keys on the path up to the root
	pathKeys, err := proto.PathNodeKeys(state.Lk, copathNodes)
	if err != nil {
		mu.Die("error deriving the private path keys: %v", err)
	}

	// the initial tree key is the last key in pathKeys
	return pathKeys[len(pathKeys)-1]
}

func deriveStageKey(treeKey *ecdh.PrivateKey, m *proto.Message) []byte {
	stageInfo := proto.StageKeyInfo{
		PrevStageKey:  make([]byte, proto.StageKeySize),
		TreeSecretKey: treeKey.Bytes(),
		IKeys:         m.IKeys,
		TreeKeys:      m.TreeKeys,
	}
	stageKey, err := proto.DeriveStageKey(&stageInfo)
	if err != nil {
		mu.Die("failed to derive the stage key: %v", err)
	}

	return stageKey
}

func processMessage(opts *options, state *tree.TreeState) {
	var m proto.Message

	verifyMessage(opts.initiatorPubIKFile, opts.setupMessageFile, opts.sigFile)
	readMessage(opts.setupMessageFile, &m)

	suk := getSetupKey(&m)
	state.PublicTree = getPublicTree(&m)
	state.Lk = deriveLeafKey(opts.privEKFile, suk)
	state.IKeys = m.IKeys
	tk := deriveTreeKey(state, opts.index)
	state.Sk = deriveStageKey(tk, &m)

	fmt.Printf("Stage key: %v\n", state.Sk)
}

func parseOptions() *options {
	var err error
	opts := options{}

	flag.Usage = printUsage
	flag.StringVar(&opts.sigFile, "sig-file", "", "")
	flag.StringVar(&opts.treeStateFile, "out-state", "state.json", "")
	flag.Parse()

	if flag.NArg() != 4 {
		mu.Die(shortUsage)
	}

	opts.index, err = strconv.Atoi(flag.Arg(0))
	if err != nil {
		mu.Die("error converting positional argument INDEX to int: %v", err)
	}
	opts.privEKFile = flag.Arg(1)
	opts.initiatorPubIKFile = flag.Arg(2)
	opts.setupMessageFile = flag.Arg(3)

	if opts.sigFile == "" {
		opts.sigFile = opts.setupMessageFile + ".sig"
	}

	return &opts
}

func main() {
	opts := parseOptions()
	var state tree.TreeState

	processMessage(opts, &state)

	tree.SaveTreeState(opts.treeStateFile, &state)
}
