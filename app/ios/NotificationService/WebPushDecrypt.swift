import Foundation
import CryptoKit

enum WebPushError: Error { case malformed }

/// Decrypts an RFC 8188 aes128gcm Web Push body (RFC 8291 key derivation).
/// Mirrors `decryptWebPush` in app/lib/push/webpush_crypto.dart and
/// `encryptWebPush` in internal/push/webpush.go.
func decryptWebPush(privateKey: Data, auth: Data, body: Data) throws -> Data {
  guard body.count >= 21 else { throw WebPushError.malformed }
  let bytes = [UInt8](body)
  let salt = Data(bytes[0..<16])
  let idLen = Int(bytes[20])
  guard body.count >= 21 + idLen else { throw WebPushError.malformed }
  let asPub = Data(bytes[21..<(21 + idLen)])
  let ciphertext = Data(bytes[(21 + idLen)...])
  guard ciphertext.count >= 16 else { throw WebPushError.malformed }

  let priv = try P256.KeyAgreement.PrivateKey(rawRepresentation: privateKey)
  let uaPub = priv.publicKey.x963Representation
  let peer = try P256.KeyAgreement.PublicKey(x963Representation: asPub)
  let shared = try priv.sharedSecretFromKeyAgreement(with: peer)
  let sharedData = shared.withUnsafeBytes { Data($0) }

  var keyInfo = Data("WebPush: info\u{0}".utf8)
  keyInfo.append(uaPub)
  keyInfo.append(asPub)
  let ikm = HKDF<SHA256>.deriveKey(
    inputKeyMaterial: SymmetricKey(data: sharedData),
    salt: auth,
    info: keyInfo,
    outputByteCount: 32)

  let prk = HKDF<SHA256>.extract(inputKeyMaterial: ikm, salt: salt)
  let cek = HKDF<SHA256>.expand(
    pseudoRandomKey: prk,
    info: Data("Content-Encoding: aes128gcm\u{0}".utf8),
    outputByteCount: 16)
  let nonce = HKDF<SHA256>.expand(
    pseudoRandomKey: prk,
    info: Data("Content-Encoding: nonce\u{0}".utf8),
    outputByteCount: 12)
  let nonceData = nonce.withUnsafeBytes { Data($0) }

  let tag = ciphertext.suffix(16)
  let ct = ciphertext.prefix(ciphertext.count - 16)
  let sealed = try AES.GCM.SealedBox(
    nonce: try AES.GCM.Nonce(data: nonceData),
    ciphertext: ct,
    tag: tag)
  let record = try AES.GCM.open(sealed, using: cek)

  var i = record.count - 1
  let recBytes = [UInt8](record)
  while i >= 0 && recBytes[i] == 0x00 { i -= 1 }
  guard i >= 0 && recBytes[i] == 0x02 else { throw WebPushError.malformed }
  return Data(recBytes[0..<i])
}

func base64urlDecode(_ s: String) -> Data? {
  var t = s.replacingOccurrences(of: "-", with: "+")
           .replacingOccurrences(of: "_", with: "/")
  while t.count % 4 != 0 { t.append("=") }
  return Data(base64Encoded: t)
}
