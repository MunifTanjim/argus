import Foundation
import UserNotifications

/// Composites a notification's `node_id` and `session_id` into the `node:session`
/// id the app uses everywhere. Falls back to the bare session id when no node is
/// present. Kept identical to the Dart compositing so delivered notifications
/// match the id the app dismisses.
func compositeSessionId(from info: [AnyHashable: Any]) -> String? {
  guard let session = info["session_id"] as? String, !session.isEmpty else { return nil }
  if let node = info["node_id"] as? String, !node.isEmpty {
    return "\(node):\(session)"
  }
  return session
}

func deliveredIdentifiers(
  matching sessionId: String,
  in delivered: [(identifier: String, userInfo: [AnyHashable: Any])]
) -> [String] {
  delivered.compactMap {
    compositeSessionId(from: $0.userInfo) == sessionId ? $0.identifier : nil
  }
}

/// Whether a foreground push for [info] must be suppressed because its session is the
/// one on screen ([activeSessionId]). Mirrors the Dart suppressForActive so iOS and
/// Android suppress the same foreground pushes.
func suppressForActive(_ activeSessionId: String?, info: [AnyHashable: Any]) -> Bool {
  guard let active = activeSessionId, let sid = compositeSessionId(from: info)
  else { return false }
  return sid == active
}

/// The presentation options for a foreground push. Returns an empty set when the
/// push must be suppressed because its session is on screen. Otherwise returns
/// banner, list, and sound. The `.list` option is required so the notification
/// enters Notification Center; `.banner` alone only shows the banner.
func foregroundPresentationOptions(
  _ activeSessionId: String?, info: [AnyHashable: Any]
) -> UNNotificationPresentationOptions {
  if suppressForActive(activeSessionId, info: info) {
    return []
  }
  return [.banner, .list, .sound]
}

/// Removes every delivered notification for [sessionId]. APNs owns the delivered
/// notification's identifier, so the match is by composited session id, not by a
/// known id.
func removeDeliveredNotifications(
  matching sessionId: String,
  from center: UNUserNotificationCenter = .current(),
  completion: (() -> Void)? = nil
) {
  center.getDeliveredNotifications { notifications in
    let delivered = notifications.map {
      (identifier: $0.request.identifier, userInfo: $0.request.content.userInfo)
    }
    let ids = deliveredIdentifiers(matching: sessionId, in: delivered)
    if !ids.isEmpty {
      center.removeDeliveredNotifications(withIdentifiers: ids)
    }
    completion?()
  }
}
