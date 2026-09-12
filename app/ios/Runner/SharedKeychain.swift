import Foundation

enum SharedKeychain {
  static let accessGroup = "group.dev.muniftanjim.argus"
  static let service = "dev.muniftanjim.argus.webpush"
  static let account = "webpush_keys"

  static func write(priv: String, auth: String) -> Bool {
    let payload = try? JSONSerialization.data(
      withJSONObject: ["priv": priv, "auth": auth])
    guard let data = payload else { return false }
    let base: [String: Any] = [
      kSecClass as String: kSecClassGenericPassword,
      kSecAttrService as String: service,
      kSecAttrAccount as String: account,
      kSecAttrAccessGroup as String: accessGroup,
    ]
    SecItemDelete(base as CFDictionary)
    var add = base
    add[kSecValueData as String] = data
    add[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlock
    return SecItemAdd(add as CFDictionary, nil) == errSecSuccess
  }
}
