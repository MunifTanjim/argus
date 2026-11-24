import Cocoa
import FlutterMacOS

class MainFlutterWindow: NSWindow {
  // performDrag needs the mouse-down event, but the Dart call arrives after
  // the pan gesture starts, when NSApp.currentEvent is no longer the mouse-down.
  private var lastMouseDown: NSEvent?
  private var mouseMonitor: Any?

  override func awakeFromNib() {
    let flutterViewController = FlutterViewController()
    let windowFrame = self.frame
    self.contentViewController = flutterViewController
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

  deinit {
    if let monitor = mouseMonitor { NSEvent.removeMonitor(monitor) }
  }
}
