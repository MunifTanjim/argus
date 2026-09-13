import UserNotifications
import XCTest

@testable import Runner

final class SessionNotificationsTests: XCTestCase {
  func testCompositeUsesNodeAndSession() {
    XCTAssertEqual(compositeSessionId(from: ["session_id": "s1", "node_id": "n1"]), "n1:s1")
  }

  func testCompositeFallsBackToBareSession() {
    XCTAssertEqual(compositeSessionId(from: ["session_id": "s1"]), "s1")
    XCTAssertEqual(compositeSessionId(from: ["session_id": "s1", "node_id": ""]), "s1")
  }

  func testCompositeNilWithoutSession() {
    XCTAssertNil(compositeSessionId(from: ["node_id": "n1"]))
    XCTAssertNil(compositeSessionId(from: ["session_id": ""]))
  }

  func testDeliveredIdentifiersMatchByComposite() {
    let delivered: [(identifier: String, userInfo: [AnyHashable: Any])] = [
      (identifier: "a", userInfo: ["session_id": "s1", "node_id": "n1"]),
      (identifier: "b", userInfo: ["session_id": "s2", "node_id": "n1"]),
      (identifier: "c", userInfo: ["session_id": "s1", "node_id": "n1"]),
    ]
    XCTAssertEqual(deliveredIdentifiers(matching: "n1:s1", in: delivered), ["a", "c"])
  }

  func testDeliveredIdentifiersEmptyWithoutMatch() {
    let delivered: [(identifier: String, userInfo: [AnyHashable: Any])] = [
      (identifier: "a", userInfo: ["session_id": "s2", "node_id": "n1"]),
    ]
    XCTAssertEqual(deliveredIdentifiers(matching: "n1:s1", in: delivered), [])
  }

  func testSuppressForActiveMatchesComposite() {
    XCTAssertTrue(
      suppressForActive("n1:s1", info: ["session_id": "s1", "node_id": "n1"]))
  }

  func testSuppressForActiveIgnoresOtherSession() {
    XCTAssertFalse(
      suppressForActive("n1:s1", info: ["session_id": "s2", "node_id": "n1"]))
  }

  func testSuppressForActiveNilWhenNoActive() {
    XCTAssertFalse(
      suppressForActive(nil, info: ["session_id": "s1", "node_id": "n1"]))
  }

  func testSuppressForActiveNilWhenNoSessionInfo() {
    XCTAssertFalse(suppressForActive("n1:s1", info: ["node_id": "n1"]))
  }

  func testPresentationOptionsIncludeListWhenNotActive() {
    let options = foregroundPresentationOptions(
      nil, info: ["session_id": "s1", "node_id": "n1"])
    XCTAssertTrue(options.contains(.banner))
    XCTAssertTrue(options.contains(.list))
    XCTAssertTrue(options.contains(.sound))
  }

  func testPresentationOptionsIncludeListForOtherSession() {
    let options = foregroundPresentationOptions(
      "n1:s1", info: ["session_id": "s2", "node_id": "n1"])
    XCTAssertTrue(options.contains(.list))
  }

  func testPresentationOptionsEmptyForActiveSession() {
    let options = foregroundPresentationOptions(
      "n1:s1", info: ["session_id": "s1", "node_id": "n1"])
    XCTAssertTrue(options.isEmpty)
  }
}
