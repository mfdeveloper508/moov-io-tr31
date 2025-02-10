package encryption

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// HeaderError is a custom error type that indicates an error in processing TR-31 header data.
type HeaderError struct {
	message string
}

// KeyBlockError is a custom error type that indicates an error in processing TR-31 key block data.
type KeyBlockError struct {
	message string
}

type Blocks struct {
	_blocks map[string]string
}

type Header struct {
	VersionID                string
	KeyUsage                 string
	Algorithm                string
	ModeOfUse                string
	VersionNum               string
	Exportability            string
	Reserved                 string
	Blocks                   Blocks
	_versionIDAlgoBlockSize  map[string]int
	_versionIDKeyBlockMacLen map[string]int
}

type KeyBlock struct {
	kbpk   []byte
	header *Header
}

// NewHeaderError is a constructor function to create a new HeaderError.
func NewHeaderError(message string) *HeaderError {
	return &HeaderError{message: message}
}

// Error method to implement the error interface for HeaderError.
func (e *HeaderError) Error() string {
	return fmt.Sprintf("HeaderError: %s", e.message)
}

// NewKeyBlockError is a constructor function to create a new KeyBlockError.
func NewKeyBlockError(message string) *KeyBlockError {
	return &KeyBlockError{message: message}
}

// Error method to implement the error interface for KeyBlockError.
func (e *KeyBlockError) Error() string {
	return fmt.Sprintf("KeyBlockError: %s", e.message)
}

func NewBlocks() *Blocks {
	return &Blocks{
		_blocks: make(map[string]string),
	}
}

func (b *Blocks) Len() int {
	return len(b._blocks)
}

func (b *Blocks) Get(key string) (string, error) {
	if value, exists := b._blocks[key]; exists {
		return value, nil
	}
	return "", errors.New("key not found")
}

func (b *Blocks) Set(key string, item string) error {
	if len(key) != 2 || !asciiAlphanumeric(key) {
		return &HeaderError{
			message: fmt.Sprintf("Block ID (%s) is invalid. Expecting 2 alphanumeric characters.", key),
		}
	}
	if !asciiPrintable(item) {
		return &HeaderError{
			message: fmt.Sprintf("Block %s data is invalid. Expecting ASCII printable characters. Data: '%s'", key, item),
		}
	}
	b._blocks[key] = item
	return nil
}

func (b *Blocks) Delete(key string) {
	delete(b._blocks, key)
}

func (b *Blocks) Iter() chan string {
	ch := make(chan string)
	go func() {
		for key := range b._blocks {
			ch <- key
		}
		close(ch)
	}()
	return ch
}

func (b *Blocks) Contains(key string) bool {
	_, exists := b._blocks[key]
	return exists
}

func (b *Blocks) Repr() string {
	return fmt.Sprintf("%v", b._blocks)
}

func (b *Blocks) Dump(algoBlockSize int) (int, string, error) {
	var blocksList []string
	for blockID, blockData := range b._blocks {
		blocksList = append(blocksList, blockID)

		if len(blockData)+4 <= 255 {
			length := len(blockData) + 4
			byteSlice := []byte{byte(length)}
			hexString := hex.EncodeToString(byteSlice)
			blocksList = append(blocksList, hexString)
		} else {
			blocksList = append(blocksList, "0002")
			blockLen := len(blockData) + 10
			if blockLen > 0xFFFF {
				return 0, "", &HeaderError{message: fmt.Sprintf("Block %s length is too long.", blockID)}
			}
			blocksList = append(blocksList, fmt.Sprintf("%04X", blockLen))
		}
		blocksList = append(blocksList, blockData)
	}

	blocks := strings.Join(blocksList, "")

	if len(blocks) > 0 && algoBlockSize > 0 && len(blocks)%algoBlockSize != 0 {
		padNum := algoBlockSize - ((len(blocks) + 4) % algoBlockSize)
		pbBlock := "PB" + fmt.Sprintf("%02X", 4+padNum) + strings.Repeat("0", padNum)
		return len(b._blocks) + 1, blocks + pbBlock, nil
	}

	return len(b._blocks), blocks, nil
}

// Parse the extended length of a block.
func (b *Blocks) parseExtendedLen(blockID string, blocks string, i int) (int, int, error) {
	// Get 2 character long optional block length of length.
	if len(blocks) < i+2 {
		return 0, i, &HeaderError{
			message: fmt.Sprintf("Block %s length of length (%s) is malformed. Expecting 2 hexchars.", blockID, blocks[i:]),
		}
	}
	blockLenLenS := blocks[i : i+2]
	if len(blockLenLenS) != 2 || !isAsciiHex(blockLenLenS) {
		return 0, i, &HeaderError{
			message: fmt.Sprintf("Block %s length of length (%s) is malformed. Expecting 2 hexchars.", blockID, blockLenLenS),
		}
	}
	i += 2

	// Convert length to integer (in hex), and multiply by 2 to get the byte length.
	blockLenLen, err := strconv.ParseInt(blockLenLenS, 16, 0)
	if err != nil {
		return 0, i, &HeaderError{
			message: fmt.Sprintf("Failed to parse block length length (%s) for block %s: %v", blockLenLenS, blockID, err),
		}
	}
	blockLenLen *= 2

	// Ensure blockLenLen is not zero.
	if blockLenLen == 0 {
		return 0, i, &HeaderError{
			message: fmt.Sprintf("Block %s length of length must not be 0.", blockID),
		}
	}
	if len(blocks) < i+int(blockLenLen) {
		var msg string
		if len(blocks) > i {
			msg = fmt.Sprintf("Block %s length (%s) is malformed. Expecting 2 hexchars.", blockID, blocks[i:])
		} else {
			msg = fmt.Sprintf("Block %s length (%s) is malformed. Expecting 2 hexchars.", blockID, "")
		}
		return 0, i, &HeaderError{msg}
	}
	// Extract actual block length.
	blockLenS := blocks[i : i+int(blockLenLen)]
	if len(blockLenS) != int(blockLenLen) || !isAsciiHex(blockLenS) {
		return 0, i, &HeaderError{
			message: fmt.Sprintf("Block %s length (%s) is malformed. Expecting %d hexchars.", blockID, blockLenS, blockLenLen),
		}
	}

	// Convert block length to integer.
	blockLen, err := strconv.ParseInt(blockLenS, 16, 0)
	if err != nil {
		return 0, i, &HeaderError{
			message: fmt.Sprintf("Failed to parse block length (%s) for block %s: %v", blockLenS, blockID, err),
		}
	}

	// Block length includes ID, 00 length indicator, length of length and actual length in it.
	// Remove that to return block data length.
	blockDataLen := int(blockLen) - 6 - int(blockLenLen)
	return blockDataLen, i + int(blockLenLen), nil
}

func (b *Blocks) Load(blocksNum int, blocks string) (int, error) {
	b._blocks = make(map[string]string)

	i := 0
	for j := 0; j < blocksNum; j++ {
		if len(blocks) < 1 {
			return 0, &HeaderError{message: fmt.Sprintf("Block ID () is malformed.")}
		}
		if len(blocks) < 2 || len(blocks[:2]) != 2 {
			return 0, &HeaderError{message: fmt.Sprintf("Block ID (%v) is malformed.", blocks[i:i+1])}
		}
		if len(blocks) < i+2 {
			return 0, &HeaderError{message: fmt.Sprintf("Block ID (%v) is malformed.", blocks[i:i+1])}
		}
		blockID := blocks[i : i+2]
		i += 2
		if !asciiAlphanumeric(blockID) {
			return 0, &HeaderError{message: fmt.Sprintf("Block ID (%v) is invalid. Expecting 2 alphanumeric characters.", blockID)}
		}
		if len(blocks) < i+4 {
			return 0, &HeaderError{message: fmt.Sprintf("Block %s length (%s) is malformed. Expecting 2 hexchars.", blockID, blocks[i:])}
		}
		blockLenS := blocks[i : i+2]
		i += 2

		blockLen := hexToInt(blockLenS)
		if blockLen == 0 {
			// Handle extended length
			// Add logic to parse extended length if necessary
			block_len_extend, new_index, err := b.parseExtendedLen(blockID, blocks, i)
			if err != nil {
				return 0, err
			}
			blockLen = block_len_extend
			i = new_index
		} else {
			blockLen -= 4
		}

		if blockLen < 0 {
			return 0, &HeaderError{message: fmt.Sprintf("Block %s length does not include block ID and length.", blockID)}
		}
		if len(blocks) < i+blockLen {
			return 0, &HeaderError{fmt.Sprintf("Block %s data is malformed. Received %d/%d. Block data: '%s'", blockID, len(blocks)-i, blockLen, blocks[i:])}
		}
		blockData := blocks[i : i+blockLen]
		if len(blockData) != blockLen {
			return 0, &HeaderError{message: fmt.Sprintf("Block %s data is malformed. Received %d/%d. Block data: '%s'", blockID, len(blockData), blockLen, blockData)}
		}
		i += blockLen

		if blockID != "PB" {
			b._blocks[blockID] = blockData
		}
	}

	return i, nil
}
func DefaultHeader() *Header {
	header := &Header{
		VersionID:                "B",
		KeyUsage:                 "00",
		Algorithm:                "0",
		ModeOfUse:                "0",
		VersionNum:               "00",
		Exportability:            "N",
		Reserved:                 "00",
		Blocks:                   *NewBlocks(),
		_versionIDAlgoBlockSize:  map[string]int{"A": 8, "B": 8, "C": 8, "D": 16},
		_versionIDKeyBlockMacLen: map[string]int{"A": 4, "B": 8, "C": 4, "D": 16},
	}
	return header
}
func NewHeader(versionID, keyUsage, algorithm, modeOfUse, versionNum, exportability string) (*Header, error) {
	header := &Header{
		VersionID:                "",
		KeyUsage:                 "",
		Algorithm:                "",
		ModeOfUse:                "",
		VersionNum:               "",
		Exportability:            "",
		Reserved:                 "00",
		Blocks:                   *NewBlocks(),
		_versionIDAlgoBlockSize:  map[string]int{"A": 8, "B": 8, "C": 8, "D": 16},
		_versionIDKeyBlockMacLen: map[string]int{"A": 4, "B": 8, "C": 4, "D": 16},
	}
	err := header.SetVersionID(versionID)
	if err != nil {
		return nil, err
	}
	err = header.SetKeyUsage(keyUsage)
	if err != nil {
		return nil, err
	}
	err = header.SetAlgorithm(algorithm)
	if err != nil {
		return nil, err
	}
	err = header.SetModeOfUse(modeOfUse)
	if err != nil {
		return nil, err
	}
	err = header.SetVersionNum(versionNum)
	if err != nil {
		return nil, err
	}
	err = header.SetExportability(exportability)
	if err != nil {
		return nil, err
	}
	return header, nil
}
func (h *Header) String() string {
	blocksNum, blocks, _ := h.Blocks.Dump(h._versionIDAlgoBlockSize[h.VersionID])
	return fmt.Sprintf("%s%04d%s%s%s%s%s%02d%s%s", h.VersionID, 16+len(blocks), h.KeyUsage, h.Algorithm, h.ModeOfUse, h.VersionNum, h.Exportability, blocksNum, h.Reserved, blocks)
}
func (h *Header) SetVersionID(versionID string) error {
	if versionID != "A" && versionID != "B" && versionID != "C" && versionID != "D" {
		return &HeaderError{message: fmt.Sprintf("Version ID (%s) is not supported.", versionID)}
	}
	h.VersionID = versionID
	return nil
}
func (h *Header) SetKeyUsage(keyUsage string) error {
	if len(keyUsage) != 2 || !asciiAlphanumeric(keyUsage) {
		return &HeaderError{message: fmt.Sprintf("Key usage (%s) is invalid.", keyUsage)}
	}
	h.KeyUsage = keyUsage
	return nil
}
func (h *Header) SetAlgorithm(algorithm string) error {
	if len(algorithm) != 1 || !asciiAlphanumeric(algorithm) {
		return &HeaderError{message: fmt.Sprintf("Algorithm (%s) is invalid.", algorithm)}
	}
	h.Algorithm = algorithm
	return nil
}
func (h *Header) SetModeOfUse(modeOfUse string) error {
	if len(modeOfUse) != 1 || !asciiAlphanumeric(modeOfUse) {
		return &HeaderError{message: fmt.Sprintf("Mode of use (%s) is invalid.", modeOfUse)}
	}
	h.ModeOfUse = modeOfUse
	return nil
}
func (h *Header) SetVersionNum(versionNum string) error {
	if len(versionNum) != 2 || !asciiAlphanumeric(versionNum) {
		return &HeaderError{message: fmt.Sprintf("Version number (%s) is invalid.", versionNum)}
	}
	h.VersionNum = versionNum
	return nil
}

func (h *Header) SetExportability(exportability string) error {
	if len(exportability) != 1 || !asciiAlphanumeric(exportability) {
		return &HeaderError{message: fmt.Sprintf("Exportability (%s) is invalid.", exportability)}
	}
	h.Exportability = exportability
	return nil
}
func (h *Header) GetBlocks() map[string]string {
	return h.Blocks._blocks
}

func (h *Header) Dump(keyLen int) (string, error) {
	algoBlockSize := h._versionIDAlgoBlockSize[h.VersionID]
	padLen := algoBlockSize - ((2 + keyLen) % algoBlockSize)
	blocksNum, blocks, _ := h.Blocks.Dump(algoBlockSize)

	kbLen := 16 + 4 + (keyLen * 2) + (padLen * 2) + (h._versionIDKeyBlockMacLen[h.VersionID] * 2) + len(blocks)

	if kbLen > 9999 {
		return "", &HeaderError{message: fmt.Sprintf("Total key block length (%d) exceeds limit of 9999.", kbLen)}
	}

	return fmt.Sprintf("%s%04d%s%s%s%s%s%02d%s%s", h.VersionID, kbLen, h.KeyUsage, h.Algorithm, h.ModeOfUse, h.VersionNum, h.Exportability, blocksNum, h.Reserved, blocks), nil
}

func (h *Header) Load(header string) (int, error) {
	if !asciiAlphanumeric(header[:16]) {
		return 0, &HeaderError{message: fmt.Sprintf("Header must be ASCII alphanumeric. Header: '%s'", header[:16])}
	}

	if len(header) < 16 {
		return 0, &HeaderError{message: fmt.Sprintf("Header length (%d) must be >=16. Header: '%s'", len(header), header[:16])}
	}
	err := h.SetVersionID(string(header[0]))
	if err != nil {
		return 0, err
	}
	err = h.SetKeyUsage(header[5:7])
	if err != nil {
		return 0, err
	}
	err = h.SetAlgorithm(string(header[7]))
	if err != nil {
		return 0, err
	}
	err = h.SetModeOfUse(string(header[8]))
	if err != nil {
		return 0, err
	}
	err = h.SetVersionNum(header[9:11])
	if err != nil {
		return 0, err
	}
	err = h.SetExportability(string(header[11]))
	if err != nil {
		return 0, err
	}
	h.Reserved = header[14:16]

	if !asciiNumeric(header[12:14]) {
		return 0, &HeaderError{message: fmt.Sprintf("Number of blocks (%s) is invalid. Expecting 2 digits.", header[12:14])}
	}

	blocksNum := int(header[12]-'0')*10 + int(header[13]-'0')
	blocksLen, err := h.Blocks.Load(blocksNum, header[16:])
	return 16 + blocksLen, err
}

var _versionIDKeyBlockMacLen = map[string]int{
	"A": 4,
	"B": 8,
	"C": 4,
	"D": 16,
}

var _versionIDAlgoBlockSize = map[string]int{
	"A": 8,
	"B": 8,
	"C": 8,
	"D": 16,
}

var _algoIDMaxKeyLen = map[string]int{
	"T": 24,
	"D": 24,
	"A": 32,
}

func NewKeyBlock(kbpk []byte, header interface{}) (*KeyBlock, error) {
	// Validate the input for kbpk and header
	if len(kbpk) == 0 {
		return nil, errors.New("Key Block Protection Key (KBPK) cannot be empty")
	}

	kb := &KeyBlock{
		kbpk: kbpk,
	}

	if iheader, ok := header.(*Header); ok {
		kb.header = iheader
	} else if iheader, ok := header.(string); ok {
		kb.header = DefaultHeader()
		if len(iheader) < 5 {
		} else if _, err := kb.header.Load(iheader); err != nil {
			return nil, fmt.Errorf("failed to load header: %v", err)
		}
	} else {
		kb.header = DefaultHeader()
	}
	return kb, nil
}

func (kb *KeyBlock) String() string {
	return fmt.Sprintf("%v", kb.header)
}
func (kb *KeyBlock) GetHeader() *Header {
	return kb.header
}
func (kb *KeyBlock) Wrap(key []byte, maskedKeyLen *int) (string, error) {
	// Check if header version is supported
	wrapFunc, exists := _wrapDispatch[kb.header.VersionID]
	if !exists {
		return "", fmt.Errorf("Key block version ID (%s) is not supported", kb.header.VersionID)
	}

	// If maskedKeyLen is nil, use max key size for the algorithm
	wrappedMaskedLen := 0
	if maskedKeyLen == nil {
		if maxLen, exists := _algoIDMaxKeyLen[kb.header.Algorithm]; exists {
			// Use the max key length for the algorithm
			wrappedMaskedLen = max(maxLen, len(key))
		} else {
			wrappedMaskedLen = len(key)
		}
	} else {
		wrappedMaskedLen = max(*maskedKeyLen, len(key))
	}
	maskedKeyLen = &wrappedMaskedLen
	// Call the wrap function based on the header's versionID
	headerDump, _ := kb.header.Dump(*maskedKeyLen)
	wrapData, err := wrapFunc(kb, headerDump, key, *maskedKeyLen-len(key))
	return wrapData, err
}
func (kb *KeyBlock) Unwrap(keyBlock string) ([]byte, error) {
	// Extract header from the key block
	if len(keyBlock) < 5 {
		return nil, &KeyBlockError{
			message: fmt.Sprintf("Key block header length is malformed. Expecting 4 digits."),
		}
	}
	headerLen, _ := kb.header.Load(keyBlock)

	// Verify block length
	if !asciiNumeric(keyBlock[1:5]) {
		return nil, &KeyBlockError{
			message: fmt.Sprintf("Key block header length (%s) is malformed. Expecting 4 digits.", keyBlock[1:5]),
		}
	}

	keyBlockLen := stringToInt(keyBlock[1:5])
	if keyBlockLen != len(keyBlock) {
		return nil, &KeyBlockError{
			message: fmt.Sprintf("Key block header length (%d) doesn't match input data length (%d).", keyBlockLen, len(keyBlock)),
		}
	}

	// Check if the length is multiple of the required block size
	blockSize := _versionIDAlgoBlockSize[kb.header.VersionID]
	if len(keyBlock)%blockSize != 0 {
		return nil, &KeyBlockError{
			message: fmt.Sprintf("Key block length (%d) must be multiple of %d for key block version %s.", len(keyBlock), blockSize, kb.header.VersionID),
		}
	}

	// Extract MAC from the key block
	algoMacLen := _versionIDKeyBlockMacLen[kb.header.VersionID]

	keyBlockBytes := []byte(keyBlock)
	if headerLen < len(keyBlockBytes) {
		// Correct slice calculation to avoid out of bounds
		receivedMacS := keyBlockBytes[headerLen:]
		if len(receivedMacS) > algoMacLen*2 {
			receivedMacS = receivedMacS[len(receivedMacS)-algoMacLen*2:]
			receivedMac, err := hex.DecodeString(string(receivedMacS))
			if err != nil {
				return nil, &KeyBlockError{
					message: fmt.Sprintf("Key block MAC must be valid hexchars. MAC: '%s'", receivedMacS),
				}
			}

			if len(receivedMac) != algoMacLen {
				return nil, &KeyBlockError{
					message: fmt.Sprintf("Key block MAC is malformed. Received %d bytes MAC. Expecting %d bytes for key block version %s. MAC: '%s'", len(receivedMacS), algoMacLen*2, kb.header.VersionID, receivedMacS),
				}
			}

			// Extract encrypted key data from the key block
			keyDataS := keyBlockBytes[headerLen:]
			keyDataS = keyDataS[:len(keyDataS)-algoMacLen*2]
			keyDataS_S := string(keyDataS)
			if len(keyDataS_S) > 0 {

			}
			keyData, err := hex.DecodeString(string(keyDataS))
			if err != nil {
				return nil, &KeyBlockError{
					message: fmt.Sprintf("Encrypted key must be valid hexchars. Key data: '%s'", keyDataS),
				}
			}

			// Call unwrap function based on version ID
			unwrapFunc, exists := _unwrapDispatch[kb.header.VersionID]
			if !exists {
				return nil, &KeyBlockError{
					message: fmt.Sprintf("Key block version ID (%s) is not supported.", kb.header.VersionID),
				}
			}

			unwrapData, err := unwrapFunc(kb, keyBlock[:headerLen], keyData, receivedMac)
			return unwrapData, err
		} else {
			// Handle case where the slice is too short
			return nil, &KeyBlockError{
				message: fmt.Sprintf("Key block MAC must be valid hexchars. MAC: '%s'", receivedMacS),
			}
		}
	} else {
		return nil, &KeyBlockError{
			message: fmt.Sprintf("headerLen is out of bounds"),
		}
	}
}

type WrapFunc func(keyBlock *KeyBlock, header string, key []byte, extraPad int) (string, error)
type UnwrapFunc func(keyBlock *KeyBlock, str string, data []byte, mac []byte) ([]byte, error)

// Define the dispatch maps for wrap and unwrap functions
var _wrapDispatch = map[string]WrapFunc{
	"A": (*KeyBlock).CWrap,
	"B": (*KeyBlock).BWrap,
	"C": (*KeyBlock).CWrap,
	"D": (*KeyBlock).DWrap,
}

var _unwrapDispatch = map[string]UnwrapFunc{
	"A": (*KeyBlock).CUnwrap,
	"B": (*KeyBlock).BUnwrap,
	"C": (*KeyBlock).CUnwrap,
	"D": (*KeyBlock).DUnwrap,
}

// Version B
func (kb *KeyBlock) BWrap(header string, key []byte, extraPad int) (string, error) {
	// Ensure KBPK length is valid
	if len(kb.kbpk) != 16 && len(kb.kbpk) != 24 {
		return "", &KeyBlockError{
			message: fmt.Sprintf("KBPK length (%d) must be Double or Triple DES for key block version %s.", len(kb.kbpk), kb.header.VersionID),
		}
	}

	// Derive Key Block Encryption and Authentication Keys
	kbek, kbak, _ := kb.BDerive()

	// Format key data: 2-byte key length measured in bits + key + pad
	padLen := 8 - ((2 + len(key) + extraPad) % 8)
	pad := make([]byte, padLen+extraPad)
	_, err := rand.Read(pad)
	if err != nil {
		return "", err
	}

	// Clear key data
	clearKeyData := make([]byte, 2+len(key)+len(pad))
	binary.BigEndian.PutUint16(clearKeyData[:2], uint16(len(key)*8))
	copy(clearKeyData[2:], key)
	copy(clearKeyData[2+len(key):], pad)

	// Generate MAC
	mac, _ := kb.bGenerateMac(kbak, header, clearKeyData)

	// Encrypt key data using TDES CBC
	encKey, err := EncryptTDESCBC(kbek, mac, clearKeyData)
	if err != nil {
		return "", err
	}

	// Return the concatenated result
	return header + hex.EncodeToString(encKey) + hex.EncodeToString(mac), nil
}

// Derive Key Block Encryption and Authentication Keys
func (kb *KeyBlock) BDerive() ([]byte, []byte, error) {
	// Key Derivation data
	// byte 0 = a counter increment for each block of kbpk, start at 1
	// byte 1-2 = key usage indicator
	//   - 0000 = encryption
	//   - 0001 = MAC
	// byte 3 = separator, set to 0
	// byte 4-5 = algorithm indicator
	//   - 0000 = 2-Key TDES
	//   - 0001 = 3-Key TDES
	// byte 6-7 = key length in bits
	//   - 0080 = 2-Key TDES
	//   - 00C0 = 3-Key TDES
	kdInput := []byte{0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x80}

	var callsToCmac []int
	if len(kb.kbpk) == 16 {
		// Adjust for 2-key TDES
		kdInput[4], kdInput[5] = 0x00, 0x00
		kdInput[6], kdInput[7] = 0x00, 0x80
		callsToCmac = []int{1, 2}
	} else {
		// Adjust for 3-key TDES
		kdInput[4], kdInput[5] = 0x00, 0x01
		kdInput[6], kdInput[7] = 0x00, 0xC0
		callsToCmac = []int{1, 2, 3}
	}

	var kbek, kbak []byte // Encryption key and authentication key

	// Generate CMAC for the KBPK
	k1, _, err := kb.deriveDesCmacSubkey(kb.kbpk)
	if err != nil {
		return nil, nil, err
	}

	// Produce the same number of keying material as the key's length
	// Each call to CMAC produces 64 bits of keying material
	for _, i := range callsToCmac {
		// Increment counter for each call to CMAC
		kdInput[0] = byte(i)

		// Encryption key
		kdInput[1], kdInput[2] = 0x00, 0x00
		encKey, err := generateCBCMAC(kb.kbpk, xor(kdInput, k1), 1, 8, DES)
		if err != nil {
			return nil, nil, err
		}
		kbek = append(kbek, encKey...)

		// Authentication key
		kdInput[1], kdInput[2] = 0x00, 0x01
		authKey, err := generateCBCMAC(kb.kbpk, xor(kdInput, k1), 1, 8, DES)
		if err != nil {
			return nil, nil, err
		}
		kbak = append(kbak, authKey...)
	}

	return kbek, kbak, nil
}
func (kb *KeyBlock) bGenerateMac(kbak []byte, header string, keyData []byte) ([]byte, error) {
	// Derive the CMAC subkey using KBAK
	km1, _, err := kb.deriveDesCmacSubkey(kbak)
	if err != nil {
		return nil, err
	}

	// Combine the header and key data
	macData := []byte(header)
	macData = append(macData, keyData...)

	// Modify the last 8 bytes of macData by XOR'ing with km1
	if len(macData) >= 8 {
		macData = append(macData[:len(macData)-8], xor(macData[len(macData)-8:], km1)...)
	} else {
		return nil, &KeyBlockError{message: fmt.Sprintf("macData is too short for the XOR operation")}
	}

	// Generate the CBC-MAC
	mac, err := generateCBCMAC(kbak, macData, 1, 8, DES)
	if err != nil {
		return nil, err
	}

	return mac, nil
}
func shiftLeft1(inBytes []byte) []byte {
	// Shift the byte array left by 1 bit
	result := make([]byte, len(inBytes))
	copy(result, inBytes)
	result[0] = result[0] & 0b01111111
	intIn := bytesToInt(result) << 1
	return intToBytes(int(intIn), len(inBytes))
}

// _derive_des_cmac_subkey derives two subkeys (k1, k2) from a DES key
func (kb *KeyBlock) deriveDesCmacSubkey(key []byte) ([]byte, []byte, error) {
	// Define the constant for the shifting operation
	r64 := []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x1B}

	// Encrypt the key using TDES ECB (this is a placeholder for actual TDES ECB encryption)
	s, err := EncryptTDSECB(key, []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
	if err != nil {
		return nil, nil, err
	}

	// Derive k1
	var k1 []byte
	if s[0]&0b10000000 != 0 {
		k1 = xor(shiftLeft1(s), r64)
	} else {
		k1 = shiftLeft1(s)
	}

	// Derive k2
	var k2 []byte
	if k1[0]&0b10000000 != 0 {
		k2 = xor(shiftLeft1(k1), r64)
	} else {
		k2 = shiftLeft1(k1)
	}

	return k1, k2, nil
}

// _b_unwrap unwraps a key from TR-31 key block version B
func (kb *KeyBlock) BUnwrap(header string, keyData []byte, receivedMac []byte) ([]byte, error) {
	// Ensure KBPK length is valid
	if len(kb.kbpk) != 16 && len(kb.kbpk) != 24 {
		return nil, &KeyBlockError{
			message: fmt.Sprintf("KBPK length (%d) must be Double or Triple DES for key block version %s.", len(kb.kbpk), kb.header.VersionID),
		}
	}

	// Ensure the key data is valid
	if len(keyData) < 8 || len(keyData)%8 != 0 {
		return nil, &KeyBlockError{
			message: fmt.Sprintf("Encrypted key is malformed. Key data: '%s'", hex.EncodeToString(keyData)),
		}
	}

	// Derive Key Block Encryption and Authentication Keys
	kbek, kbak, err := kb.BDerive()
	if err != nil {
		return nil, err
	}

	// Decrypt key data (TDES CBC decryption)
	clearKeyData, err := DecryptTDESCBC(kbek, receivedMac, keyData)
	if err != nil {
		return nil, err
	}

	// Validate MAC
	mac, err := kb.bGenerateMac(kbak, header, clearKeyData)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(mac, receivedMac) {
		return nil, &KeyBlockError{
			message: "Key block MAC doesn't match generated MAC.",
		}
	}

	// Extract key from key data: 2-byte key length + key + pad
	keyLength := binary.BigEndian.Uint16(clearKeyData[:2])

	// Check if key length is a multiple of 8
	if keyLength%8 != 0 {
		return nil, &KeyBlockError{
			message: "Decrypted key is invalid.",
		}
	}

	// Convert to bytes
	keyLength = keyLength / 8
	if len(clearKeyData) < int(keyLength)+2 {
		return nil, &KeyBlockError{fmt.Sprintf("Decrypted key is malformed.")}
	}
	key := clearKeyData[2 : keyLength+2]
	if len(key) != int(keyLength) {
		return nil, &KeyBlockError{
			message: "Decrypted key is malformed.",
		}
	}

	return key, nil
}

// _cWrap wraps the key into a TR-31 key block version A or C.
func (kb *KeyBlock) CWrap(header string, key []byte, extraPad int) (string, error) {
	// Ensure KBPK length is valid
	if len(kb.kbpk) != 8 && len(kb.kbpk) != 16 && len(kb.kbpk) != 24 {
		return "", &KeyBlockError{
			message: fmt.Sprintf("KBPK length (%d) must be Single, Double or Triple DES for key block version %s.", len(kb.kbpk), kb.header.VersionID),
		}
	}

	// Derive Key Block Encryption and Authentication Keys
	kbek, kbak, err := kb.cDerive()
	if err != nil {
		return "", err
	}

	// Format key data: 2-byte key length measured in bits + key + pad
	padLen := 8 - ((2 + len(key) + extraPad) % 8)
	pad := make([]byte, padLen+extraPad)
	_, err = rand.Read(pad)
	if err != nil {
		return "", err
	}

	// Clear key data
	clearKeyData := make([]byte, 2+len(key)+len(pad))
	binary.BigEndian.PutUint16(clearKeyData[:2], uint16(len(key)*8))
	copy(clearKeyData[2:], key)
	copy(clearKeyData[2+len(key):], pad)

	// Encrypt key data using TDES CBC
	encKey, err := EncryptTDESCBC(kbek, []byte(header)[:8], clearKeyData)
	if err != nil {
		return "", err
	}

	// Generate MAC
	mac, err := kb.cGenerateMAC(kbak, header, encKey)
	if err != nil {
		return "", err
	}

	// Return the concatenated result
	return header + strings.ToUpper(hex.EncodeToString(encKey)) + strings.ToUpper(hex.EncodeToString(mac)), nil
}
func (kb *KeyBlock) cDerive() ([]byte, []byte, error) {
	// Create byte slices filled with 0x45 and 0x4D respectively
	encryptionKeyMask := make([]byte, len(kb.kbpk))
	authenticationKeyMask := make([]byte, len(kb.kbpk))
	for i := range kb.kbpk {
		encryptionKeyMask[i] = 0x45
		authenticationKeyMask[i] = 0x4D
	}

	// Perform XOR operation
	encryptionKey := xor(kb.kbpk, encryptionKeyMask)
	authenticationKey := xor(kb.kbpk, authenticationKeyMask)
	return encryptionKey, authenticationKey, nil
}

// cGenerateMAC generates a MAC using the provided KBAK, header, and key data.
func (kb *KeyBlock) cGenerateMAC(kbak []byte, header string, keyData []byte) ([]byte, error) {
	// Concatenate header and key data
	data := append([]byte(header), keyData...)
	encData, _ := generateCBCMAC(kbak, data, 1, 4, DES)
	// Return the last block of the encrypted data as the MAC
	return encData, nil
}

// cUnwrap unwraps the key from a TR-31 key block version A or C.
func (kb *KeyBlock) CUnwrap(header string, keyData []byte, receivedMAC []byte) ([]byte, error) {
	// Ensure KBPK length is valid (8, 16, or 24 bytes)
	if len(kb.kbpk) != 8 && len(kb.kbpk) != 16 && len(kb.kbpk) != 24 {
		return nil, &KeyBlockError{fmt.Sprintf("KBPK length (%d) must be Single, Double or Triple DES for key block version %s.", len(kb.kbpk), kb.header.VersionID)}
	}

	// Validate key data length
	if len(keyData) < 8 || len(keyData)%8 != 0 {
		return nil, &KeyBlockError{fmt.Sprintf("Encrypted key is malformed. Key data: '%X'", keyData)}
	}

	// Derive Key Block Encryption and Authentication Keys
	kbek, kbak, _ := kb.cDerive()

	// Validate MAC
	mac, _ := kb.cGenerateMAC(kbak, header, keyData)
	if !compareMAC(mac, receivedMAC) {
		return nil, &KeyBlockError{fmt.Sprintf("Key block MAC doesn't match generated MAC.")}
	}

	// Decrypt key data
	clearKeyData, err := DecryptTDESCBC(kbek, []byte(header[:8]), keyData)
	if err != nil {
		return nil, err
	}

	// Extract key from key data: 2-byte key length measured in bits + key + pad
	keyLength := binary.BigEndian.Uint16(clearKeyData[:2])

	// This library does not support keys not measured in whole bytes
	if keyLength%8 != 0 {
		return nil, &KeyBlockError{fmt.Sprintf("Decrypted key is invalid.")}
	}

	keyLength = keyLength / 8
	if len(clearKeyData) < int(keyLength)+2 {
		return nil, &KeyBlockError{fmt.Sprintf("Decrypted key is malformed.")}
	}
	key := clearKeyData[2 : keyLength+2]
	if len(key) != int(keyLength) {
		return nil, &KeyBlockError{fmt.Sprintf("Decrypted key is malformed.")}
	}

	return key, nil
}

// DWrap wraps the key into a TR-31 key block version D
func (kb *KeyBlock) DWrap(header string, key []byte, extraPad int) (string, error) {
	// Ensure KBPK length is valid
	if len(kb.kbpk) != 16 && len(kb.kbpk) != 24 && len(kb.kbpk) != 32 {
		return "", &KeyBlockError{
			message: fmt.Sprintf("KBPK length (%d) must be AES-128, AES-192 or AES-256 for key block version D.", len(kb.kbpk)),
		}
	}

	// Derive Key Block Encryption and Authentication Keys
	kbek, kbak, err := kb.dDerive()
	if err != nil {
		return "", err
	}
	// Format key data: 2-byte key length measured in bits + key + pad
	padLen := 16 - ((2 + len(key) + extraPad) % 16)
	pad := make([]byte, padLen+extraPad)
	_, err = rand.Read(pad)
	if err != nil {
		return "", err
	}

	clearKeyData := make([]byte, 2+len(key)+len(pad))
	binary.BigEndian.PutUint16(clearKeyData[:2], uint16(len(key)*8))
	copy(clearKeyData[2:], key)
	copy(clearKeyData[2+len(key):], pad)

	// Generate MAC
	mac, err := kb.dGenerateMAC(kbak, []byte(header), clearKeyData)
	if err != nil {
		return "", err
	}

	// Encrypt key data using AES CBC
	encKey, err := EncryptAESCBC(kbek, mac, clearKeyData)
	if err != nil {
		return "", err
	}

	// Return the concatenated result
	return header + hex.EncodeToString(encKey) + hex.EncodeToString(mac), nil
}
func (kb *KeyBlock) dDerive() ([]byte, []byte, error) {
	// Key Derivation data
	// byte 0 = a counter increment for each block of kbpk, start at 1
	// byte 1-2 = key usage indicator
	//   - 0000 = encryption
	//   - 0001 = MAC
	// byte 3 = separator, set to 0
	// byte 4-5 = algorithm indicator
	//   - 0002 = AES-128
	//   - 0003 = AES-192
	//   - 0004 = AES-256
	// byte 6-7 = key length in bits
	//   - 0080 = AES-128
	//   - 00C0 = AES-192
	//   - 0100 = AES-256
	kdInput := []byte{
		0x01, 0x00, 0x00, 0x00, // Counter and Key Usage Indicator
		0x00, 0x02, 0x00, 0x80, // Algorithm Indicator and Key Length
		0x80, 0x00, 0x00, 0x00, // Padding
		0x00, 0x00, 0x00, 0x00,
	}

	var callsToCmac []int
	var kbek, kbak []byte

	switch len(kb.kbpk) {
	case 16:
		// Adjust for AES 128 bit
		kdInput[4] = 0x00
		kdInput[5] = 0x02
		kdInput[6] = 0x00
		kdInput[7] = 0x80
		callsToCmac = []int{1}
	case 24:
		// Adjust for AES 192 bit
		kdInput[4] = 0x00
		kdInput[5] = 0x03
		kdInput[6] = 0x00
		kdInput[7] = 0xC0
		callsToCmac = []int{1, 2}
	case 32:
		// Adjust for AES 256 bit
		kdInput[4] = 0x00
		kdInput[5] = 0x04
		kdInput[6] = 0x01
		kdInput[7] = 0x00
		callsToCmac = []int{1, 2}
	default:
		return nil, nil, fmt.Errorf("unsupported KBPK length: %d", len(kb.kbpk))
	}

	_, k2, _ := kb.deriveAESCMACSubkeys(kb.kbpk)
	// Produce the same number of keying material as the key's length.
	// Each call to CMAC produces 128 bits of keying material.
	// AES-128 -> 1 call to CMAC  -> AES-128 KBEK/KBAK
	// AES-196 -> 2 calls to CMAC -> AES-196 KBEK/KBAK (out of 256 bits of data)
	// AES-256 -> 2 calls to CMAC -> AES-256 KBEK/KBAK
	for _, i := range callsToCmac {
		// Counter is incremented for each call to CMAC
		kdInput[0] = byte(i)

		// Encryption key
		kdInput[1] = 0x00
		kdInput[2] = 0x00
		encData, _ := generateCBCMAC(kb.kbpk, xor(kdInput, k2), 1, 16, AES)
		kbek = append(kbek, encData...)

		// Authentication key
		kdInput[1] = 0x00
		kdInput[2] = 0x01
		encData2, _ := generateCBCMAC(kb.kbpk, xor(kdInput, k2), 1, 16, AES)
		kbak = append(kbek, encData2...)
	}
	cropedKbak := kbak[len(kbak)-len(kb.kbpk):]
	return kbek[:len(kb.kbpk)], cropedKbak, nil
}
func (kb *KeyBlock) dGenerateMAC(kbak []byte, header, keyData []byte) ([]byte, error) {
	// Derive AES-CMAC subkeys
	k1, _, err := kb.deriveAESCMACSubkeys(kbak)
	if err != nil {
		return nil, err
	}

	// Concatenate header and keyData
	macData := append([]byte(header), keyData...)
	// Check if the macData length is at least 16 bytes
	if len(macData) < 16 {
		return nil, fmt.Errorf("macData is too short")
	}

	last16 := macData[len(macData)-16:]
	xored := xor(last16, k1)

	// Combine the sliced macData (without last 16 bytes) with the XORed result
	macData = append(macData[:len(macData)-16], xored...)
	return generateCBCMAC(kbak, macData, 1, 16, AES)
}
func dShiftLeft1(inBytes []byte) []byte {
	// Shift the byte array left by 1 bit
	// Ensure the most significant bit of the first byte is cleared
	copyByte := make([]byte, len(inBytes)) // Allocate memory for the destination slice
	copy(copyByte, inBytes)
	copyByte[0] &= 0b01111111

	// Convert to big integer
	intIn := new(big.Int).SetBytes(copyByte)

	// Shift left by 1
	intIn.Lsh(intIn, 1)

	// Convert back to byte slice with the same length
	outBytes := intIn.Bytes()

	// Ensure the result is the same length as input (may need padding)
	if len(outBytes) < len(copyByte) {
		padding := make([]byte, len(copyByte)-len(outBytes))
		outBytes = append(padding, outBytes...)
	}

	return outBytes
}
func (kb *KeyBlock) deriveAESCMACSubkeys(key []byte) ([]byte, []byte, error) {
	// Derive two subkeys from an AES key. Each subkey is 16 bytes.
	r64 := []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x87}
	// Encrypt a block of zeros
	zeroBytes := make([]byte, 16)
	s, err := EncryptAESECB(key, zeroBytes)
	if err != nil {
		return nil, nil, err
	}

	var k1, k2 []byte
	if s[0]&0b10000000 != 0 {
		shiteByte := dShiftLeft1(s)
		k1 = xor(shiteByte, r64)
	} else {
		k1 = dShiftLeft1(s)
	}
	if k1[0]&0b10000000 != 0 {
		k2 = xor(dShiftLeft1(k1), r64)
	} else {
		k2 = dShiftLeft1(k1)
	}
	return k1, k2, nil
}
func (kb *KeyBlock) DUnwrap(header string, keyData, receivedMAC []byte) ([]byte, error) {
	// Check for valid KBPK length (AES-128, AES-192, AES-256)
	if len(kb.kbpk) != 16 && len(kb.kbpk) != 24 && len(kb.kbpk) != 32 {
		return nil, &KeyBlockError{fmt.Sprintf(
			"KBPK length (%d) must be AES-128, AES-192 or AES-256 for key block version %s.",
			len(kb.kbpk), kb.header.VersionID,
		)}
	}

	// Check if key data length is valid
	if len(keyData) < 16 || len(keyData)%16 != 0 {
		return nil, &KeyBlockError{fmt.Sprintf("Encrypted key is malformed. Key data: '%X'", keyData)}
	}

	// Derive Key Block Encryption and Authentication Keys
	kbek, kbak, _ := kb.dDerive()
	// Decrypt key data
	clearKeyData, err := DecryptAESCBC(kbek, receivedMAC, keyData)
	if err != nil {
		return nil, err
	}

	// Validate MAC
	mac, _ := kb.dGenerateMAC(kbak, []byte(header), clearKeyData)
	if !bytes.Equal(mac, receivedMAC) {
		return nil, &KeyBlockError{fmt.Sprintf("Key block MAC doesn't match generated MAC.")}
	}

	// Extract key length from clear key data (2 byte key length in bits)
	keyLength := binary.BigEndian.Uint16(clearKeyData[:2])

	// Check if the key length is a valid multiple of 8
	if keyLength%8 != 0 {
		return nil, &KeyBlockError{fmt.Sprintf("Decrypted key is invalid.")}
	}

	// Convert key length from bits to bytes
	keyLength = keyLength / 8
	if len(clearKeyData) < int(keyLength)+2 {
		return nil, &KeyBlockError{fmt.Sprintf("Decrypted key is malformed.")}
	}
	key := clearKeyData[2 : 2+keyLength]

	// Check if key is malformed
	if len(key) != int(keyLength) {
		return nil, &KeyBlockError{fmt.Sprintf("Decrypted key is malformed.")}
	}

	return key, nil
}
