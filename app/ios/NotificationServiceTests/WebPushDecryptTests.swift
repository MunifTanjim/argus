import XCTest
@testable import NotificationService

final class WebPushDecryptTests: XCTestCase {
  func testDecryptsGoVector() throws {
    // Vector produced by internal/push/webpush.go encryptWebPush (P-256 / RFC 8291).
    let priv = base64urlDecode("eC0a-4vo68OSGuQBdM-jMeiwz72R4dfwMHp_OofoW-E")!
    let auth = base64urlDecode("I8Jbhxa6-iYjJvnD1eriOQ")!
    let body = base64urlDecode("h_6NDDD2mbjL66vhqn_oFQAAEABBBKhPWtQEnvC-1bYPMi9x1eh2JOy6DJx7JVpGFNmlJg-UWdsBGcDA5Eh8M2PlryH9UOfnqtU0t30OqKkU6uE0fLn4q0dRI9jPreDHHtQVL8DiOqIIfNL1akQ0ZfdhTN7ez75zYKT9qrKPpa3XnOiPAm3ggAfTJ1dtzihChraCJ0-sbEOOnHV8uOaw-X4uH_nhOVKbOcHx20CxM6LH7-6pbxzaFWAlGq6jIaf8LzguhiQ6")!
    let plaintext = try decryptWebPush(privateKey: priv, auth: auth, body: body)
    XCTAssertEqual(
      String(data: plaintext, encoding: .utf8),
      #"{"title":"argus","body":"A session needs your input","data":{"session_id":"s1","node_id":"n1"}}"#)
  }
}
