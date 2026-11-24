import Cocoa
import FlutterMacOS

class MainFlutterWindow: NSWindow, NSWindowDelegate {
  // performDrag needs the mouse-down event, but the Dart call arrives after
  // the pan gesture starts, when NSApp.currentEvent is no longer the mouse-down.
  private var lastMouseDown: NSEvent?
  private var mouseMonitor: Any?
  private var windowChannel: FlutterMethodChannel?

  override func awakeFromNib() {
    // The app is dark only; in the Light appearance the sidebar material
    // would be near white under the cream text.
    self.appearance = NSAppearance(named: .darkAqua)
    let flutterViewController = FlutterViewController()
    // Flutter paints the canvas itself; where it leaves the view clear (the
    // side panel), the sidebar material behind it shows through.
    flutterViewController.backgroundColor = .clear
    let effectView = NSVisualEffectView()
    effectView.material = .sidebar
    effectView.blendingMode = .behindWindow
    effectView.state = .followsWindowActiveState

    let windowFrame = self.frame
    let rootViewController = NSViewController()
    rootViewController.view = NSView(frame: self.contentRect(forFrameRect: windowFrame))
    rootViewController.addChild(flutterViewController)
    for view in [effectView, flutterViewController.view] {
      view.frame = rootViewController.view.bounds
      view.autoresizingMask = [.width, .height]
      rootViewController.view.addSubview(view)
    }
    self.contentViewController = rootViewController
    // The container view would otherwise take the first key events.
    self.makeFirstResponder(flutterViewController.view)
    self.setFrame(windowFrame, display: true)
    self.contentMinSize = NSSize(width: 420, height: 600)

    self.titlebarAppearsTransparent = true
    self.titleVisibility = .hidden
    self.styleMask.insert(.fullSizeContentView)

    mouseMonitor = NSEvent.addLocalMonitorForEvents(matching: [.leftMouseDown, .leftMouseUp]) { [weak self] event in
      if event.window === self {
        self?.lastMouseDown = event.type == .leftMouseDown ? event : nil
      }
      return event
    }

    let windowChannel = FlutterMethodChannel(
      name: "argus/window",
      binaryMessenger: flutterViewController.engine.binaryMessenger)
    self.windowChannel = windowChannel
    self.delegate = self
    windowChannel.setMethodCallHandler { [weak self] call, result in
      switch call.method {
      case "startDrag":
        if let event = self?.lastMouseDown, (NSEvent.pressedMouseButtons & 1) != 0 {
          self?.performDrag(with: event)
        }
        result(nil)
      case "doubleClick":
        // Follow the system "double-click a window's title bar" setting.
        switch UserDefaults.standard.string(forKey: "AppleActionOnDoubleClick") {
        case "Minimize": self?.performMiniaturize(nil)
        case "None": break
        default: self?.performZoom(nil)
        }
        result(nil)
      default:
        result(FlutterMethodNotImplemented)
      }
    }

    RegisterGeneratedPlugins(registry: flutterViewController)

    super.awakeFromNib()
  }

  // The strip hides in full screen, where there are no traffic lights.
  func windowWillEnterFullScreen(_ notification: Notification) {
    windowChannel?.invokeMethod("fullScreen", arguments: true)
  }

  func windowDidExitFullScreen(_ notification: Notification) {
    windowChannel?.invokeMethod("fullScreen", arguments: false)
  }

  deinit {
    if let monitor = mouseMonitor { NSEvent.removeMonitor(monitor) }
  }
}
