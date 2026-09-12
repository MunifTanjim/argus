import UserNotifications

class NotificationService: UNNotificationServiceExtension {
  var contentHandler: ((UNNotificationContent) -> Void)?
  var bestAttempt: UNMutableNotificationContent?

  override func didReceive(
    _ request: UNNotificationRequest,
    withContentHandler contentHandler: @escaping (UNNotificationContent) -> Void
  ) {
    self.contentHandler = contentHandler
    let content = (request.content.mutableCopy() as! UNMutableNotificationContent)
    self.bestAttempt = content

    guard
      let encoded = request.content.userInfo["e"] as? String,
      let body = base64urlDecode(encoded),
      let keys = readKeys(),
      let plaintext = try? decryptWebPush(
        privateKey: keys.priv, auth: keys.auth, body: body),
      let obj = try? JSONSerialization.jsonObject(with: plaintext) as? [String: Any]
    else {
      contentHandler(content)  // fall back to the placeholder alert
      return
    }

    if let title = obj["title"] as? String { content.title = title }
    if let alertBody = obj["body"] as? String { content.body = alertBody }
    // Carry data through for the tap handler in AppDelegate.
    if let data = obj["data"] as? [String: Any] {
      var info = content.userInfo
      for (k, v) in data { info[k] = v }
      content.userInfo = info
    }
    contentHandler(content)
  }

  override func serviceExtensionTimeWillExpire() {
    if let handler = contentHandler, let content = bestAttempt {
      handler(content)
    }
  }

  private func readKeys() -> (priv: Data, auth: Data)? {
    let query: [String: Any] = [
      kSecClass as String: kSecClassGenericPassword,
      kSecAttrService as String: "dev.muniftanjim.argus.webpush",
      kSecAttrAccount as String: "webpush_keys",
      kSecAttrAccessGroup as String: "group.dev.muniftanjim.argus",
      kSecReturnData as String: true,
    ]
    var out: AnyObject?
    guard SecItemCopyMatching(query as CFDictionary, &out) == errSecSuccess,
          let data = out as? Data,
          let obj = try? JSONSerialization.jsonObject(with: data) as? [String: String],
          let priv = obj["priv"].flatMap(base64urlDecode),
          let auth = obj["auth"].flatMap(base64urlDecode)
    else { return nil }
    return (priv, auth)
  }
}
