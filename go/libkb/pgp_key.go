// Copyright 2015 Keybase, Inc. All rights reserved. Use of
// this source code is governed by the included BSD license.

package libkb

import (
	"bufio"
	"bytes"
	"crypto"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/keybase/client/go/kbcrypto"
	keybase1 "github.com/keybase/client/go/protocol/keybase1"
	"github.com/keybase/go-crypto/openpgp"
	"github.com/keybase/go-crypto/openpgp/armor"
	"github.com/keybase/go-crypto/openpgp/packet"
	jsonw "github.com/keybase/go-jsonw"
	_ "golang.org/x/crypto/ripemd160" // imported so that keybase/go-crypto/openpgp supports ripemd160
)

var _ GenericKey = (*PGPKeyBundle)(nil)

type PGPKeyBundle struct {
	*openpgp.Entity

	// GPGFallbackKey to be used as a fallback if given dummy a PrivateKey.
	GPGFallbackKey GenericKey

	// We make the (fairly dangerous) assumption that the key will never be
	// modified. This avoids the issue that encoding an openpgp.Entity is
	// nondeterministic due to Go's randomized iteration order (so different
	// exports of the same key may hash differently).
	//
	// If you're *sure* that you're creating a PGPKeyBundle from an armored
	// *public* key, you can prefill this field and Export() will use it.
	ArmoredPublicKey string

	// True if this key was generated by this program
	Generated bool
}

func NewPGPKeyBundle(entity *openpgp.Entity) *PGPKeyBundle {
	return &PGPKeyBundle{Entity: entity}
}

func NewGeneratedPGPKeyBundle(entity *openpgp.Entity) *PGPKeyBundle {
	return &PGPKeyBundle{Entity: entity, Generated: true}
}

const (
	PGPFingerprintLen = 20
)

type PGPFingerprint [PGPFingerprintLen]byte

func ImportPGPFingerprint(f keybase1.PGPFingerprint) PGPFingerprint {
	var ret PGPFingerprint
	copy(ret[:], f[:])
	return ret
}

func PGPFingerprintFromHex(s string) (*PGPFingerprint, error) {
	var fp PGPFingerprint
	err := DecodeHexFixed(fp[:], []byte(s))
	switch err.(type) {
	case nil:
		return &fp, nil
	case HexWrongLengthError:
		return nil, fmt.Errorf("Bad fingerprint; wrong length: %d", len(s))
	default:
		return nil, err
	}
}

func PGPFingerprintFromSlice(b []byte) (*PGPFingerprint, error) {
	if len(b) != PGPFingerprintLen {
		return nil, fmt.Errorf("Bad fingerprint; wrong length: %d", PGPFingerprintLen)
	}
	var fp PGPFingerprint
	copy(fp[:], b)
	return &fp, nil
}

func PGPFingerprintFromHexNoError(s string) *PGPFingerprint {
	if len(s) == 0 {
		return nil
	} else if f, e := PGPFingerprintFromHex(s); e == nil {
		return f
	} else {
		return nil
	}
}

func (p PGPFingerprint) String() string {
	return hex.EncodeToString(p[:])
}

func (p PGPFingerprint) ToQuads() string {
	x := []byte(strings.ToUpper(p.String()))
	totlen := len(x)*5/4 - 1
	ret := make([]byte, totlen)
	j := 0
	for i, b := range x {
		ret[j] = b
		j++
		if (i%4) == 3 && j < totlen {
			ret[j] = ' '
			j++
		}
	}
	return string(ret)
}

func (p PGPFingerprint) ToKeyID() string {
	return strings.ToUpper(hex.EncodeToString(p[12:20]))
}

func (p PGPFingerprint) ToDisplayString(verbose bool) string {
	if verbose {
		return p.String()
	}
	return p.ToKeyID()
}

func (p *PGPFingerprint) Match(q string, exact bool) bool {
	if p == nil {
		return false
	}
	if exact {
		return strings.ToLower(p.String()) == strings.ToLower(q)
	}
	return strings.HasSuffix(strings.ToLower(p.String()), strings.ToLower(q))
}

func (k *PGPKeyBundle) InitGPGKey() {
	k.GPGFallbackKey = &GPGKey{
		fp:  k.GetFingerprintP(),
		kid: k.GetKID(),
	}
}

func (k *PGPKeyBundle) FullHash() (string, error) {
	keyBlob, err := k.Encode()
	if err != nil {
		return "", err
	}

	keySum := sha256.Sum256([]byte(strings.TrimSpace(keyBlob)))
	return hex.EncodeToString(keySum[:]), nil
}

// StripRevocations returns a copy of the key with revocations removed
func (k *PGPKeyBundle) StripRevocations() (strippedKey *PGPKeyBundle) {
	strippedKey = nil
	if k.ArmoredPublicKey != "" {
		// Re-read the key because we want to return a copy, that does
		// not reference PGPKeyBundle `k` anywhere.
		strippedKey, _, _ = ReadOneKeyFromString(k.ArmoredPublicKey)
	}

	if strippedKey == nil {
		// Either Armored key was not saved or ReadOneKeyFromString
		// failed. Do old behavior here - we won't have a proper copy
		// of the key (there is a lot of pointers in the key structs),
		// but at least we won't have to bail out completely.
		entityCopy := *k.Entity
		strippedKey = &PGPKeyBundle{Entity: &entityCopy}
	}

	strippedKey.Revocations = nil

	oldSubkeys := strippedKey.Subkeys
	strippedKey.Subkeys = nil
	for _, subkey := range oldSubkeys {
		// Skip revoked subkeys
		if subkey.Sig.SigType == packet.SigTypeSubkeyBinding && subkey.Revocation == nil {
			strippedKey.Subkeys = append(strippedKey.Subkeys, subkey)
		}
	}
	return
}

func (k *PGPKeyBundle) StoreToLocalDb(g *GlobalContext) error {
	s, err := k.Encode()
	if err != nil {
		return err
	}
	val := jsonw.NewString(s)
	g.Log.Debug("| Storing Key (kid=%s) to Local DB", k.GetKID())
	return g.LocalDb.Put(DbKey{Typ: DBPGPKey, Key: k.GetKID().String()}, []DbKey{}, val)
}

func (p PGPFingerprint) Eq(p2 PGPFingerprint) bool {
	return FastByteArrayEq(p[:], p2[:])
}

func GetPGPFingerprint(w *jsonw.Wrapper) (*PGPFingerprint, error) {
	s, err := w.GetString()
	if err != nil {
		return nil, err
	}
	return PGPFingerprintFromHex(s)
}

func GetPGPFingerprintVoid(w *jsonw.Wrapper, p *PGPFingerprint, e *error) {
	ret, err := GetPGPFingerprint(w)
	if err != nil {
		*e = err
	} else {
		*p = *ret
	}
}

func (p *PGPFingerprint) UnmarshalJSON(b []byte) error {
	tmp, err := PGPFingerprintFromHex(keybase1.Unquote(b))
	if err != nil {
		return err
	}
	*p = *tmp
	return nil
}

func (p *PGPFingerprint) MarshalJSON() ([]byte, error) {
	return keybase1.Quote(p.String()), nil
}

func (k PGPKeyBundle) toList() openpgp.EntityList {
	list := make(openpgp.EntityList, 1)
	list[0] = k.Entity
	return list
}

func (k PGPKeyBundle) GetFingerprint() PGPFingerprint {
	return PGPFingerprint(k.PrimaryKey.Fingerprint)
}

func (k PGPKeyBundle) GetFingerprintP() *PGPFingerprint {
	fp := k.GetFingerprint()
	return &fp
}

func GetPGPFingerprintFromGenericKey(k GenericKey) *PGPFingerprint {
	switch pgp := k.(type) {
	case *PGPKeyBundle:
		return pgp.GetFingerprintP()
	default:
		return nil
	}
}

func (k PGPKeyBundle) KeysById(id uint64, fp []byte) []openpgp.Key {
	return k.toList().KeysById(id, fp)
}

func (k PGPKeyBundle) KeysByIdUsage(id uint64, fp []byte, usage byte) []openpgp.Key {
	return k.toList().KeysByIdUsage(id, fp, usage)
}

func (k PGPKeyBundle) DecryptionKeys() []openpgp.Key {
	return k.toList().DecryptionKeys()
}

func (k PGPKeyBundle) MatchesKey(key *openpgp.Key) bool {
	return FastByteArrayEq(k.PrimaryKey.Fingerprint[:],
		key.Entity.PrimaryKey.Fingerprint[:])
}

func (k PGPKeyBundle) SamePrimaryAs(k2 PGPKeyBundle) bool {
	return FastByteArrayEq(k.PrimaryKey.Fingerprint[:], k2.PrimaryKey.Fingerprint[:])
}

func (k *PGPKeyBundle) Encode() (ret string, err error) {
	if k.ArmoredPublicKey != "" {
		return k.ArmoredPublicKey, nil
	}
	buf := bytes.Buffer{}
	err = k.EncodeToStream(NopWriteCloser{&buf}, false)
	if err == nil {
		ret = buf.String()
		k.ArmoredPublicKey = ret
	}
	return
}

func PGPKeyRawToArmored(raw []byte, priv bool) (ret string, err error) {

	var writer io.WriteCloser
	var out bytes.Buffer
	var which string

	if priv {
		which = "PRIVATE"
	} else {
		which = "PUBLIC"
	}
	hdr := fmt.Sprintf("PGP %s KEY BLOCK", which)

	writer, err = armor.Encode(&out, hdr, PGPArmorHeaders)

	if err != nil {
		return
	}
	if _, err = writer.Write(raw); err != nil {
		return
	}
	writer.Close()
	ret = out.String()
	return
}

func (k *PGPKeyBundle) SerializePrivate(w io.Writer) error {
	return k.Entity.SerializePrivate(w, &packet.Config{ReuseSignaturesOnSerialize: !k.Generated})
}

func (k *PGPKeyBundle) EncodeToStream(wc io.WriteCloser, private bool) error {
	// See Issue #32
	which := "PUBLIC"
	if private {
		which = "PRIVATE"
	}
	writer, err := armor.Encode(wc, fmt.Sprintf("PGP %s KEY BLOCK", which), PGPArmorHeaders)
	if err != nil {
		return err
	}

	if private {
		err = k.SerializePrivate(writer)
	} else {
		err = k.Entity.Serialize(writer)
	}
	if err != nil {
		return err
	}

	return writer.Close()
}

var cleanPGPInputRxx = regexp.MustCompile(`[ \t\r]*\n[ \t\r]*`)
var bug8612PrepassRxx = regexp.MustCompile(`^(?P<header>-{5}BEGIN PGP (.*?)-{5})(\s*(?P<junk>.+?))$`)

func cleanPGPInput(s string) string {
	s = strings.TrimSpace(s)
	v := cleanPGPInputRxx.Split(s, -1)
	ret := strings.Join(v, "\n")
	return ret
}

// note:  openpgp.ReadArmoredKeyRing only returns the first block.
// It will never return multiple entities.
func ReadOneKeyFromString(originalArmor string) (*PGPKeyBundle, *Warnings, error) {
	return readOneKeyFromString(originalArmor, false /* liberal */)
}

// bug8612Prepass cleans off any garbage trailing the "-----" in the first line of a PGP
// key. For years, the server allowed this junk through, so some keys on the server side
// (and hashed into chains) have junk here. It's pretty safe to strip it out when replaying
// sigchains, so do it.
func bug8612Prepass(a string) string {
	idx := strings.Index(a, "\n")
	if idx < 0 {
		return a
	}
	line0 := a[0:idx]
	rest := a[idx:]
	match := bug8612PrepassRxx.FindStringSubmatch(line0)
	if len(match) == 0 {
		return a
	}
	result := make(map[string]string)
	for i, name := range bug8612PrepassRxx.SubexpNames() {
		if i != 0 {
			result[name] = match[i]
		}
	}
	return result["header"] + rest
}

// note:  openpgp.ReadArmoredKeyRing only returns the first block.
// It will never return multiple entities.
func ReadOneKeyFromStringLiberal(originalArmor string) (*PGPKeyBundle, *Warnings, error) {
	return readOneKeyFromString(originalArmor, true /* liberal */)
}

func readOneKeyFromString(originalArmor string, liberal bool) (*PGPKeyBundle, *Warnings, error) {
	cleanArmor := cleanPGPInput(originalArmor)
	if liberal {
		cleanArmor = bug8612Prepass(cleanArmor)
	}
	reader := strings.NewReader(cleanArmor)
	el, err := openpgp.ReadArmoredKeyRing(reader)
	return finishReadOne(el, originalArmor, err)
}

// firstPrivateKey scans s for a private key block.
func firstPrivateKey(s string) (string, error) {
	scanner := bufio.NewScanner(strings.NewReader(s))
	var lines []string
	looking := true
	complete := false
	for scanner.Scan() {
		line := scanner.Text()
		if looking && strings.HasPrefix(line, "-----BEGIN PGP PRIVATE KEY BLOCK-----") {
			looking = false

		}
		if looking {
			continue
		}
		lines = append(lines, line)
		if strings.HasPrefix(line, "-----END PGP PRIVATE KEY BLOCK-----") {
			complete = true
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if looking {
		// never found a private key block
		return "", NoSecretKeyError{}
	}
	if !complete {
		// string ended without the end tag
		return "", errors.New("never found end block line")
	}
	return strings.Join(lines, "\n"), nil
}

// ReadPrivateKeyFromString finds the first private key block in s
// and decodes it into a PGPKeyBundle.  It is useful in the case
// where s contains multiple key blocks and you want the private
// key block.  For example, the result of gpg export.
func ReadPrivateKeyFromString(s string) (*PGPKeyBundle, *Warnings, error) {
	priv, err := firstPrivateKey(s)
	if err != nil {
		return nil, &Warnings{}, err
	}
	return ReadOneKeyFromString(priv)
}

func mergeKeysIfPossible(out *PGPKeyBundle, lst []*openpgp.Entity) error {
	for _, e := range lst {
		tmp := PGPKeyBundle{Entity: e}
		if out.SamePrimaryAs(tmp) {
			out.MergeKey(&tmp)
		} else {
			return TooManyKeysError{len(lst) + 1}
		}
	}
	return nil
}

func finishReadOne(lst []*openpgp.Entity, armored string, err error) (*PGPKeyBundle, *Warnings, error) {
	w := &Warnings{}
	if err != nil {
		return nil, w, err
	}
	if len(lst) == 0 {
		return nil, w, NoKeyError{"No keys found in primary bundle"}
	}
	first := &PGPKeyBundle{Entity: lst[0]}

	if len(lst) > 1 {

		// Some keys like Sheldon Hern's (https://github.com/keybase/client/issues/2130)
		// have the same primary key twice in their list of keys. In this case, we should just
		// perform a merge if possible, since the server-side accepts and merges such key exports.
		err = mergeKeysIfPossible(first, lst[1:])
		if err != nil {
			return nil, w, err
		}
	}

	for _, bs := range first.Entity.BadSubkeys {
		w.Push(Warningf("Bad subkey: %s", bs.Err))
	}

	if first.Entity.PrivateKey == nil {
		first.ArmoredPublicKey = armored
	}
	return first, w, nil
}

func ReadOneKeyFromBytes(b []byte) (*PGPKeyBundle, *Warnings, error) {
	reader := bytes.NewBuffer(b)
	el, err := openpgp.ReadKeyRing(reader)
	return finishReadOne(el, "", err)
}

func GetOneKey(jw *jsonw.Wrapper) (*PGPKeyBundle, *Warnings, error) {
	s, err := jw.GetString()
	if err != nil {
		return nil, &Warnings{}, err
	}
	return ReadOneKeyFromString(s)
}

// XXX for now this is OK but probably we need a PGP uid parser
// as in pgp-utils
func (k *PGPKeyBundle) FindKeybaseUsername(un string) bool {

	rxx := regexp.MustCompile("(?i)< " + un + "@keybase.io>$")

	for _, id := range k.Identities {
		if rxx.MatchString(id.Name) {
			return true
		}
	}
	return false
}

func (k PGPKeyBundle) VerboseDescription() string {
	lines := k.UsersDescription()
	lines = append(lines, k.KeyDescription())
	return strings.Join(lines, "\n")
}

func (k PGPKeyBundle) HumanDescription() string {
	user := k.GetPrimaryUID()
	keyID := k.GetFingerprint().ToKeyID()
	return fmt.Sprintf("PGP key %s %s", user, keyID)
}

func (k PGPKeyBundle) UsersDescription() []string {
	id := k.GetPrimaryUID()
	if len(id) == 0 {
		return nil
	}
	return []string{"user: " + id}
}

// GetPrimaryUID gets the primary UID in the given key bundle, returned
// in the 'Max K (foo) <bar@baz.com>' convention.
func (k PGPKeyBundle) GetPrimaryUID() string {

	var pri *openpgp.Identity
	var s string
	if len(k.Identities) == 0 {
		return ""
	}
	var first *openpgp.Identity
	for _, id := range k.Identities {
		if first == nil {
			first = id
		}
		if id.SelfSignature != nil && id.SelfSignature.IsPrimaryId != nil && *id.SelfSignature.IsPrimaryId {
			pri = id
			break
		}
	}
	if pri == nil {
		pri = first
	}
	if pri.UserId != nil {
		s = pri.UserId.Id
	} else {
		s = pri.Name
	}
	return s
}

// HasSecretKey checks if the PGPKeyBundle contains secret key. This
// function returning true does not indicate that the key is
// functional - it may also be a key stub.
func (k *PGPKeyBundle) HasSecretKey() bool {
	return k.PrivateKey != nil
}

// FindPGPPrivateKey checks if supposed secret key PGPKeyBundle
// contains any valid PrivateKey entities. Sometimes primary private
// key is stoopped out but there are subkeys with secret keys.
func FindPGPPrivateKey(k *PGPKeyBundle) bool {
	if k.PrivateKey.PrivateKey != nil {
		return true
	}

	for _, subKey := range k.Subkeys {
		if subKey.PrivateKey != nil && subKey.PrivateKey.PrivateKey != nil {
			return true
		}
	}

	return false
}

func (k *PGPKeyBundle) CheckSecretKey() (err error) {
	if k.PrivateKey == nil {
		err = NoSecretKeyError{}
	} else if k.PrivateKey.Encrypted {
		err = kbcrypto.BadKeyError{Msg: "PGP key material should be unencrypted"}
	} else if !FindPGPPrivateKey(k) && k.GPGFallbackKey == nil {
		err = kbcrypto.BadKeyError{Msg: "no private key material or GPGKey"}
	}
	return
}

func (k *PGPKeyBundle) CanSign() bool {
	return (k.PrivateKey != nil && !k.PrivateKey.Encrypted) || k.GPGFallbackKey != nil
}

func (k *PGPKeyBundle) GetBinaryKID() keybase1.BinaryKID {

	prefix := []byte{
		byte(kbcrypto.KeybaseKIDV1),
		byte(k.PrimaryKey.PubKeyAlgo),
	}

	// XXX Hack;  Because PublicKey.serializeWithoutHeaders is off-limits
	// to us, we need to do a full serialize and then strip off the header.
	// The further annoyance is that the size of the header varies with the
	// bitlen of the key.  Small keys (<191 bytes total) yield 8 bytes of header
	// material --- for instance, 1024-bit test keys.  For longer keys, we
	// have 9 bytes of header material, to encode a 2-byte frame, rather than
	// a 1-byte frame.
	buf := bytes.Buffer{}
	_ = k.PrimaryKey.Serialize(&buf)
	byts := buf.Bytes()
	hdrBytes := 8
	if len(byts) >= 193 {
		hdrBytes++
	}
	sum := sha256.Sum256(buf.Bytes()[hdrBytes:])

	out := append(prefix, sum[:]...)
	out = append(out, byte(kbcrypto.IDSuffixKID))

	return keybase1.BinaryKID(out)
}

func (k *PGPKeyBundle) GetKID() keybase1.KID {
	return k.GetBinaryKID().ToKID()
}

func (k PGPKeyBundle) GetAlgoType() kbcrypto.AlgoType {
	return kbcrypto.AlgoType(k.PrimaryKey.PubKeyAlgo)
}

func (k PGPKeyBundle) KeyDescription() string {
	algo, kid, creation := k.KeyInfo()
	return fmt.Sprintf("%s, ID %s, created %s", algo, kid, creation)
}

func (k PGPKeyBundle) KeyInfo() (algorithm, kid, creation string) {
	pubkey := k.PrimaryKey

	var typ string
	switch pubkey.PubKeyAlgo {
	case packet.PubKeyAlgoRSA, packet.PubKeyAlgoRSAEncryptOnly, packet.PubKeyAlgoRSASignOnly:
		typ = "RSA"
	case packet.PubKeyAlgoDSA:
		typ = "DSA"
	case packet.PubKeyAlgoECDSA:
		typ = "ECDSA"
	case packet.PubKeyAlgoEdDSA:
		typ = "EdDSA"
	default:
		typ = "<UNKNOWN TYPE>"
	}

	bl, err := pubkey.BitLength()
	if err != nil {
		bl = 0
	}

	algorithm = fmt.Sprintf("%d-bit %s key", bl, typ)
	kid = pubkey.KeyIdString()
	creation = pubkey.CreationTime.Format("2006-01-02")

	return
}

// Generates hash security warnings given a CKF
func (k PGPKeyBundle) SecurityWarnings(kind HashSecurityWarningType) (warnings HashSecurityWarnings) {
	fingerprint := k.GetFingerprint()
	for _, identity := range k.Entity.Identities {
		if identity.SelfSignature == nil ||
			IsHashSecure(identity.SelfSignature.Hash) {
			continue
		}

		warnings = append(
			warnings,
			NewHashSecurityWarning(
				kind,
				identity.SelfSignature.Hash,
				&fingerprint,
			),
		)
		return
	}
	return
}

func unlockPrivateKey(k *packet.PrivateKey, pw string) error {
	if !k.Encrypted {
		return nil
	}
	err := k.Decrypt([]byte(pw))
	if err != nil && strings.HasSuffix(err.Error(), "private key checksum failure") {
		// XXX this is gross, the openpgp library should return a better
		// error if the PW was incorrectly specified
		err = PassphraseError{}
	}
	return err
}

func (k *PGPKeyBundle) isAnyKeyEncrypted() bool {
	if k.PrivateKey.Encrypted {
		return true
	}

	for _, subkey := range k.Subkeys {
		if subkey.PrivateKey.Encrypted {
			return true
		}
	}

	return false
}

func (k *PGPKeyBundle) unlockAllPrivateKeys(pw string) error {
	if err := unlockPrivateKey(k.PrivateKey, pw); err != nil {
		return err
	}
	for _, subkey := range k.Subkeys {
		if err := unlockPrivateKey(subkey.PrivateKey, pw); err != nil {
			return err
		}
	}
	return nil
}

func (k *PGPKeyBundle) Unlock(m MetaContext, reason string, secretUI SecretUI) error {
	if !k.isAnyKeyEncrypted() {
		m.Debug("Key is not encrypted, skipping Unlock.")
		return nil
	}

	unlocker := func(pw string, _ bool) (ret GenericKey, err error) {
		if err = k.unlockAllPrivateKeys(pw); err != nil {
			return nil, err
		}
		return k, nil
	}

	_, err := NewKeyUnlocker(5, reason, k.VerboseDescription(), PassphraseTypePGP, false, secretUI, unlocker).Run(m)
	return err
}

func (k *PGPKeyBundle) CheckFingerprint(fp *PGPFingerprint) error {
	if k == nil {
		return UnexpectedKeyError{}
	}
	if fp == nil {
		return UnexpectedKeyError{}
	}
	fp2 := k.GetFingerprint()
	if !fp2.Eq(*fp) {
		return BadFingerprintError{fp2, *fp}
	}
	return nil
}

func (k *PGPKeyBundle) SignToString(msg []byte) (sig string, id keybase1.SigID, err error) {
	if sig, id, err = SimpleSign(msg, *k); err != nil && k.GPGFallbackKey != nil {
		return k.GPGFallbackKey.SignToString(msg)
	}
	return
}

func (k PGPKeyBundle) VerifyStringAndExtract(ctx VerifyContext, sig string) (msg []byte, res SigVerifyResult, err error) {
	var ps *ParsedSig
	if ps, err = PGPOpenSig(sig); err != nil {
		return nil, SigVerifyResult{}, err
	} else if err = ps.Verify(k); err != nil {
		ctx.Debug("Failing key----------\n%s", k.ArmoredPublicKey)
		ctx.Debug("Failing sig----------\n%s", sig)
		return nil, SigVerifyResult{}, err
	}
	msg = ps.LiteralData
	res.SigID = ps.ID()
	if ps.MD.Signature.Hash == crypto.SHA1 {
		res.WeakDigest = &ps.MD.Signature.Hash
	}
	return msg, res, nil
}

func (k PGPKeyBundle) VerifyString(ctx VerifyContext, sig string, msg []byte) (res SigVerifyResult, err error) {
	fmt.Printf("PGPKeyBundle::VerifyString: %s\n%s\n%s\n", k.Entity.PrimaryKey.KeyIdString(), sig, string(msg))
	extractedMsg, res, err := k.VerifyStringAndExtract(ctx, sig)
	if err != nil {
		return SigVerifyResult{}, err
	}
	if !FastByteArrayEq(extractedMsg, msg) {
		err = BadSigError{"wrong payload"}
		return SigVerifyResult{}, err
	}
	return res, nil
}

func IsPGPAlgo(algo kbcrypto.AlgoType) bool {
	switch algo {
	case kbcrypto.KIDPGPRsa, kbcrypto.KIDPGPElgamal, kbcrypto.KIDPGPDsa, kbcrypto.KIDPGPEcdh, kbcrypto.KIDPGPEcdsa, kbcrypto.KIDPGPBase, kbcrypto.KIDPGPEddsa:
		return true
	}
	return false
}

func (k *PGPKeyBundle) FindEmail(em string) bool {
	for _, ident := range k.Identities {
		if i, e := ParseIdentity(ident.Name); e == nil && i.Email == em {
			return true
		}
	}
	return false
}

func (k *PGPKeyBundle) IdentityNames() []string {
	var names []string
	for _, ident := range k.Identities {
		names = append(names, ident.Name)
	}
	return names
}

func (k *PGPKeyBundle) GetPGPIdentities() []keybase1.PGPIdentity {
	ret := make([]keybase1.PGPIdentity, len(k.Identities))
	for _, pgpIdentity := range k.Identities {
		ret = append(ret, ExportPGPIdentity(pgpIdentity))
	}
	return ret
}

// CheckIdentity finds the foo_user@keybase.io PGP identity and figures out when it
// was created and when it's slated to expire. We plan to start phasing out use of
// PGP-specified Expiration times as far as sigchain walking is concerned. But for now,
// there are a few places where it's still used (see ComputedKeyInfos#InsertServerEldestKey).
func (k *PGPKeyBundle) CheckIdentity(kbid Identity) (match bool, ctime int64, etime int64) {
	ctime, etime = -1, -1
	for _, pgpIdentity := range k.Identities {
		if Cicmp(pgpIdentity.UserId.Email, kbid.Email) {
			match = true
			ctime = pgpIdentity.SelfSignature.CreationTime.Unix()
			// This is a special case in OpenPGP, so we used KeyLifetimeSecs
			lifeSeconds := pgpIdentity.SelfSignature.KeyLifetimeSecs
			if lifeSeconds == nil {
				// No expiration time is OK, it just means it never expires.
				etime = 0
			} else {
				etime = ctime + int64(*lifeSeconds)
			}
			break
		}
	}
	return
}

// EncryptToString fails for this type of key, since we haven't implemented it yet
func (k *PGPKeyBundle) EncryptToString(plaintext []byte, sender GenericKey) (ciphertext string, err error) {
	err = KeyCannotEncryptError{}
	return
}

// DecryptFromString fails for this type of key, since we haven't implemented it yet
func (k *PGPKeyBundle) DecryptFromString(ciphertext string) (msg []byte, sender keybase1.KID, err error) {
	err = KeyCannotDecryptError{}
	return
}

// CanEncrypt returns false for now, since we haven't implemented PGP encryption of packets
// for metadata operations
func (k *PGPKeyBundle) CanEncrypt() bool { return false }

// CanDecrypt returns false for now, since we haven't implemented PGP encryption of packets
// for metadata operations
func (k *PGPKeyBundle) CanDecrypt() bool { return false }

func (k *PGPKeyBundle) ExportPublicAndPrivate() (public RawPublicKey, private RawPrivateKey, err error) {
	var publicKey, privateKey bytes.Buffer

	serializePublic := func() error { return k.Entity.Serialize(&publicKey) }
	serializePrivate := func() error { return k.SerializePrivate(&privateKey) }

	// NOTE(maxtaco): For imported keys, it is crucial to serialize the public key
	// **before** the private key, since the latter operation destructively
	// removes signature subpackets from the key serialization.
	// This was the cause of keybase/keybase-issues#1906.
	//
	// Urg, there's still more.  For generated keys, it's the opposite.
	// We have to sign the key components first (via SerializePrivate)
	// so we can export them publicly.

	if k.Generated {
		err = serializePrivate()
		if err == nil {
			err = serializePublic()
		}
	} else {
		err = serializePublic()

		if err == nil {
			err = serializePrivate()
		}
	}

	if err != nil {
		return nil, nil, err
	}

	return RawPublicKey(publicKey.Bytes()), RawPrivateKey(privateKey.Bytes()), nil
}

func (k *PGPKeyBundle) SecretSymmetricKey(reason EncryptionReason) (NaclSecretBoxKey, error) {
	return NaclSecretBoxKey{}, KeyCannotEncryptError{}
}

//===================================================

// Fulfill the TrackIdComponent interface

func (p PGPFingerprint) ToIDString() string {
	return p.String()
}

func (p PGPFingerprint) ToKeyValuePair() (string, string) {
	return PGPAssertionKey, p.ToIDString()
}

func (p PGPFingerprint) GetProofState() keybase1.ProofState {
	return keybase1.ProofState_OK
}

func (p PGPFingerprint) LastWriterWins() bool {
	return false
}

func (p PGPFingerprint) GetProofType() keybase1.ProofType {
	return keybase1.ProofType_PGP
}

//===================================================

func EncryptPGPKey(bundle *openpgp.Entity, passphrase string) error {
	passBytes := []byte(passphrase)

	if bundle.PrivateKey != nil && bundle.PrivateKey.PrivateKey != nil {
		// Primary private key exists and is not stubbed.
		if err := bundle.PrivateKey.Encrypt(passBytes, nil); err != nil {
			return err
		}
	}

	for _, subkey := range bundle.Subkeys {
		if subkey.PrivateKey == nil || subkey.PrivateKey.PrivateKey == nil {
			// There has to be a private key and not stubbed.
			continue
		}

		if err := subkey.PrivateKey.Encrypt(passBytes, nil); err != nil {
			return err
		}
	}

	return nil
}
