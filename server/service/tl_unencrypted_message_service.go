package service

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/rockin0098/flash/base/crypto"
	"github.com/rockin0098/flash/proto/mtproto"
	"github.com/rockin0098/flash/server/model"
)

func (s *TLService) TL_req_pq_Process(sess *Session, msg *mtproto.UnencryptedMessage) (interface{}, error) {
	Log.Infof("entering... sessid = %v", sess.SessionID)

	tlobj := msg.TLObject
	tl := tlobj.(*mtproto.TL_req_pq)
	nonce := tl.Get_nonce()
	if nonce == nil || len(nonce) != 16 {
		Log.Warnf("invalid nonce = %v", hex.EncodeToString(nonce))
		return nil, fmt.Errorf("req_pq invalid nonce : %v", nonce)
	}

	Log.Infof("nonce = %v", hex.EncodeToString(nonce))

	mtp := sess.MTProto
	mtpcryptor := mtp.Cryptor
	state := mtp.State

	resPQ := &mtproto.TL_resPQ{
		M_nonce:                          nonce,
		M_server_nonce:                   crypto.GenerateNonce(16),
		M_pq:                             mtpcryptor.PQ,
		M_server_public_key_fingerprints: []int64{int64(mtpcryptor.Fingerprint)},
	}

	// sess 缓存 nonce
	state.Nonce = resPQ.Get_nonce()
	state.ServerNonce = resPQ.Get_server_nonce()

	return resPQ, nil
}

func (s *TLService) TL_req_DH_params_Process(sess *Session, msg *mtproto.UnencryptedMessage) (interface{}, error) {
	Log.Infof("entering... sessid = %v", sess.SessionID)

	tlobj := msg.TLObject
	tl := tlobj.(*mtproto.TL_req_DH_params)

	mtp := sess.MTProto
	state := mtp.State
	cryptor := mtp.Cryptor

	if !bytes.Equal(tl.Get_nonce(), state.Nonce) {
		Log.Warnf("nonce not match, tl.nonce = %v, state.nonce = %v",
			hex.EncodeToString(tl.Get_nonce()), hex.EncodeToString(state.Nonce))
		return nil, errors.New("nonce not match")
	}

	Log.Infof("tl.nonce = %v, state.nonce = %v",
		hex.EncodeToString(tl.Get_nonce()), hex.EncodeToString(state.Nonce))

	Log.Infof("tl.server_nonce = %v, state.ServerNonce = %v",
		hex.EncodeToString(tl.Get_server_nonce()), hex.EncodeToString(state.ServerNonce))

	Log.Infof("tl.p = %v, state.p = %v",
		hex.EncodeToString([]byte(tl.Get_p())), hex.EncodeToString(cryptor.P))

	Log.Infof("tl.q = %v, state.q = %v",
		hex.EncodeToString([]byte(tl.Get_q())), hex.EncodeToString(cryptor.Q))

	Log.Infof("tl.fingerprint = %v, state.fingerprint = %v",
		tl.Get_public_key_fingerprint(), int64(cryptor.Fingerprint))

	// new_nonce := another (good) random number generated by the client;
	// after this query, it is known to both client and server;
	//
	// data := a serialization of
	//
	// p_q_inner_data#83c95aec pq:string p:string q:string nonce:int128 server_nonce:int128 new_nonce:int256 = P_Q_inner_data
	// or of
	// p_q_inner_data_temp#3c6a84d4 pq:string p:string q:string nonce:int128 server_nonce:int128 new_nonce:int256 expires_in:int = P_Q_inner_data;
	//
	// data_with_hash := SHA1(data) + data + (any random bytes);
	// 	 such that the length equal 255 bytes;
	// encrypted_data := RSA (data_with_hash, server_public_key);
	// 	 a 255-byte long number (big endian) is raised to the requisite power over the requisite modulus,
	// 	 and the result is stored as a 256-byte number.
	//

	// 1. 解密
	encryptedPQInnerData := cryptor.RSA.Decrypt([]byte(tl.Get_encrypted_data()))

	// 2. 反序列化出pqInnerData
	pqInnerData := mtproto.New_TL_p_q_inner_data()
	err := pqInnerData.Decode(encryptedPQInnerData[mtproto.SHA_DIGEST_LENGTH+4:])
	if err != nil {
		Log.Errorf("process Req_DHParams - TLPQInnerData decode error: %v", err)
		return nil, fmt.Errorf("process Req_DHParams - TLPQInnerData decode error: %v", err)
	}

	// 比较 pqInnerData 中的信息
	Log.Infof("pqInnerData.nonce = %v, state.nonce = %v",
		hex.EncodeToString(pqInnerData.Get_nonce()), hex.EncodeToString(state.Nonce))

	Log.Infof("pqInnerData.server_nonce = %v, state.ServerNonce = %v",
		hex.EncodeToString(pqInnerData.Get_server_nonce()), hex.EncodeToString(state.ServerNonce))

	Log.Infof("pqInnerData.p = %v, state.p = %v",
		hex.EncodeToString([]byte(pqInnerData.Get_p())), hex.EncodeToString(cryptor.P))

	Log.Infof("pqInnerData.q = %v, state.q = %v",
		hex.EncodeToString([]byte(pqInnerData.Get_q())), hex.EncodeToString(cryptor.Q))

	Log.Infof("pqInnerData.pq = %v, state.pq = %v",
		[]byte(pqInnerData.Get_pq()), []byte(cryptor.PQ))

	state.NewNonce = pqInnerData.Get_new_nonce()
	state.A = crypto.GenerateNonce(256)
	state.P = cryptor.DH2048_P

	bigIntA := new(big.Int).SetBytes(state.A)

	// 服务端计算GA = g^a mod p
	g_a := new(big.Int)
	g_a.Exp(cryptor.BigIntDH2048_G, bigIntA, cryptor.BigIntDH2048_P)

	// server inner data
	server_DHInnerData := &mtproto.TL_server_DH_inner_data{
		M_nonce:        state.Nonce,
		M_server_nonce: state.ServerNonce,
		M_g:            int32(cryptor.DH2048_G[0]),
		M_g_a:          string(g_a.Bytes()),
		M_dh_prime:     string(cryptor.DH2048_P),
		M_server_time:  int32(time.Now().Unix()),
	}

	server_DHInnerData_bytes := server_DHInnerData.Encode()

	// 创建aes和iv key
	tmp_aes_key_and_iv := make([]byte, 64)
	sha1_a := sha1.Sum(append(state.NewNonce, state.ServerNonce...))
	sha1_b := sha1.Sum(append(state.ServerNonce, state.NewNonce...))
	sha1_c := sha1.Sum(append(state.NewNonce, state.NewNonce...))
	copy(tmp_aes_key_and_iv, sha1_a[:])
	copy(tmp_aes_key_and_iv[20:], sha1_b[:])
	copy(tmp_aes_key_and_iv[40:], sha1_c[:])
	copy(tmp_aes_key_and_iv[60:], state.NewNonce[:4])

	tmpLen := 20 + len(server_DHInnerData_bytes)
	if tmpLen%16 > 0 {
		tmpLen = (tmpLen/16 + 1) * 16
	} else {
		tmpLen = 20 + len(server_DHInnerData_bytes)
	}

	tmp_encrypted_answer := make([]byte, tmpLen)
	sha1_tmp := sha1.Sum(server_DHInnerData_bytes)
	copy(tmp_encrypted_answer, sha1_tmp[:])
	copy(tmp_encrypted_answer[20:], server_DHInnerData_bytes)

	e := crypto.NewAES256IGECryptor(tmp_aes_key_and_iv[:32], tmp_aes_key_and_iv[32:64])
	tmp_encrypted_answer, _ = e.Encrypt(tmp_encrypted_answer)

	server_DHParamsOk := &mtproto.TL_server_DH_params_ok{
		M_nonce:            state.Nonce,
		M_server_nonce:     state.ServerNonce,
		M_encrypted_answer: string(tmp_encrypted_answer),
	}

	return server_DHParamsOk, nil
}

func (s *TLService) TL_set_client_DH_params_Process(sess *Session, msg *mtproto.UnencryptedMessage) (interface{}, error) {
	Log.Infof("entering... sessid = %v", sess.SessionID)

	tlobj := msg.TLObject
	tl := tlobj.(*mtproto.TL_set_client_DH_params)

	mtp := sess.MTProto
	state := mtp.State
	cryptor := mtp.Cryptor

	if !bytes.Equal(tl.Get_nonce(), state.Nonce) {
		Log.Warnf("nonce not match, tl.nonce = %v, state.nonce = %v",
			hex.EncodeToString(tl.Get_nonce()), hex.EncodeToString(state.Nonce))
		return nil, errors.New("nonce not match")
	}

	Log.Infof("tl.nonce = %v, state.nonce = %v",
		hex.EncodeToString(tl.Get_nonce()), hex.EncodeToString(state.Nonce))

	Log.Infof("tl.server_nonce = %v, state.ServerNonce = %v",
		hex.EncodeToString(tl.Get_server_nonce()), hex.EncodeToString(state.ServerNonce))

	bEncryptedData := []byte(tl.Get_encrypted_data())

	// 创建aes和iv key
	tmp_aes_key_and_iv := make([]byte, 64)
	sha1_a := sha1.Sum(append(state.NewNonce, state.ServerNonce...))
	sha1_b := sha1.Sum(append(state.ServerNonce, state.NewNonce...))
	sha1_c := sha1.Sum(append(state.NewNonce, state.NewNonce...))
	copy(tmp_aes_key_and_iv, sha1_a[:])
	copy(tmp_aes_key_and_iv[20:], sha1_b[:])
	copy(tmp_aes_key_and_iv[40:], sha1_c[:])
	copy(tmp_aes_key_and_iv[60:], state.NewNonce[:4])

	d := crypto.NewAES256IGECryptor(tmp_aes_key_and_iv[:32], tmp_aes_key_and_iv[32:64])
	decryptedData, err := d.Decrypt(bEncryptedData)
	if err != nil {
		err := fmt.Errorf("process SetClient_DHParams - AES256IGECryptor descrypt error")
		Log.Error(err)
		return nil, err
	}

	client_DHInnerData := mtproto.New_TL_client_DH_inner_data()
	err = client_DHInnerData.Decode(decryptedData[24:])
	if err != nil {
		Log.Errorf("processSetClient_DHParams - TLClient_DHInnerData decode error: %s", err)
		return nil, err
	}

	// Log.Info("processSetClient_DHParams - client_DHInnerData: ", client_DHInnerData.String())

	Log.Infof("client_DHInnerData.nonce = %v, state.nonce = %v",
		hex.EncodeToString(client_DHInnerData.Get_nonce()), hex.EncodeToString(state.Nonce))

	Log.Infof("client_DHInnerData.server_nonce = %v, state.ServerNonce = %v",
		hex.EncodeToString(client_DHInnerData.Get_server_nonce()), hex.EncodeToString(state.ServerNonce))

	bigIntA := new(big.Int).SetBytes(state.A)

	// hash_key
	authKeyNum := new(big.Int)
	authKeyNum.Exp(new(big.Int).SetBytes([]byte(client_DHInnerData.Get_g_b())), bigIntA, cryptor.BigIntDH2048_P)

	authKey := make([]byte, 256)

	// TODO(@benqi): dhGenRetry and dhGenFail
	copy(authKey[256-len(authKeyNum.Bytes()):], authKeyNum.Bytes())
	authKeyAuxHash := make([]byte, len(state.NewNonce))
	copy(authKeyAuxHash, state.NewNonce)
	authKeyAuxHash = append(authKeyAuxHash, byte(0x01))
	sha1_d := sha1.Sum(authKey)
	authKeyAuxHash = append(authKeyAuxHash, sha1_d[:]...)
	sha1_e := sha1.Sum(authKeyAuxHash[:len(authKeyAuxHash)-12])
	authKeyAuxHash = append(authKeyAuxHash, sha1_e[:]...)

	// 至此key已经创建成功
	authKeyID := int64(binary.LittleEndian.Uint64(authKeyAuxHash[len(state.NewNonce)+1+12 : len(state.NewNonce)+1+12+8]))

	dhGenOk := &mtproto.TL_dh_gen_ok{
		M_nonce:           state.Nonce,
		M_server_nonce:    state.ServerNonce,
		M_new_nonce_hash1: authKeyAuxHash[len(authKeyAuxHash)-16 : len(authKeyAuxHash)],
	}

	state.AuthKeyID = authKeyID
	state.AuthKey = authKey

	m := &model.AuthKey{
		AuthID: authKeyID,
		Body:   hex.EncodeToString(authKey), //base64.RawStdEncoding.EncodeToString(authKey),
	}

	mm := model.GetModelManager()
	err = mm.ModelAdd(m)
	if err != nil {
		Log.Error("save authkey failed, sessid=%v, err=%v", sess.SessionID, err)
		return nil, err
	}

	return dhGenOk, nil
}
