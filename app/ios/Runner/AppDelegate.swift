import Flutter
import UIKit
import UserNotifications

@main
@objc class AppDelegate: FlutterAppDelegate, FlutterImplicitEngineDelegate {
  private var channel: FlutterMethodChannel?
  private var pendingToken: String?
  private var tokenResult: FlutterResult?
  private var launchSessionId: String?

  override func application(
    _ application: UIApplication,
    didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]?
  ) -> Bool {
    UNUserNotificationCenter.current().delegate = self
    UNUserNotificationCenter.current().requestAuthorization(
      options: [.alert, .badge, .sound]
    ) { granted, _ in
      if granted {
        DispatchQueue.main.async { application.registerForRemoteNotifications() }
      }
    }
    return super.application(application, didFinishLaunchingWithOptions: launchOptions)
  }

  func didInitializeImplicitFlutterEngine(_ engineBridge: FlutterImplicitEngineBridge) {
    GeneratedPluginRegistrant.register(with: engineBridge.pluginRegistry)
    let ch = FlutterMethodChannel(
      name: "dev.muniftanjim.argus/apns",
      binaryMessenger: engineBridge.pluginRegistry.registrar(forPlugin: "ArgusApns")!.messenger()
    )
    ch.setMethodCallHandler { [weak self] call, result in
      self?.handle(call, result)
    }
    self.channel = ch
  }

  private func handle(_ call: FlutterMethodCall, _ result: @escaping FlutterResult) {
    switch call.method {
    case "getApnsToken":
      if let token = pendingToken {
        result(token)
      } else {
        tokenResult?(FlutterError(code: "superseded", message: nil, details: nil))
        tokenResult = result
      }
    case "writeWebPushKeys":
      guard let args = call.arguments as? [String: Any],
            let priv = args["priv"] as? String,
            let auth = args["auth"] as? String else {
        result(false); return
      }
      result(SharedKeychain.write(priv: priv, auth: auth))
    case "getLaunchSessionId":
      result(launchSessionId)
      launchSessionId = nil
    default:
      result(FlutterMethodNotImplemented)
    }
  }

  override func application(
    _ application: UIApplication,
    didRegisterForRemoteNotificationsWithDeviceToken deviceToken: Data
  ) {
    let token = deviceToken.map { String(format: "%02x", $0) }.joined()
    pendingToken = token
    tokenResult?(token)
    tokenResult = nil
  }

  override func application(
    _ application: UIApplication,
    didFailToRegisterForRemoteNotificationsWithError error: Error
  ) {
    tokenResult?(FlutterError(
      code: "apns_registration_failed",
      message: error.localizedDescription,
      details: nil))
    tokenResult = nil
  }

  override func userNotificationCenter(
    _ center: UNUserNotificationCenter,
    willPresent notification: UNNotification,
    withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void
  ) {
    completionHandler([.banner, .sound])
  }

  override func userNotificationCenter(
    _ center: UNUserNotificationCenter,
    didReceive response: UNNotificationResponse,
    withCompletionHandler completionHandler: @escaping () -> Void
  ) {
    let info = response.notification.request.content.userInfo
    if let sessionId = compositeSessionId(from: info) {
      if channel == nil {
        launchSessionId = sessionId
      } else {
        channel?.invokeMethod("onTap", arguments: sessionId)
      }
    }
    completionHandler()
  }

  private func compositeSessionId(from info: [AnyHashable: Any]) -> String? {
    guard let session = info["session_id"] as? String, !session.isEmpty else { return nil }
    if let node = info["node_id"] as? String, !node.isEmpty {
      return "\(node):\(session)"
    }
    return session
  }
}
