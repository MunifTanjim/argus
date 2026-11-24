import Cocoa
import FlutterMacOS

class MainFlutterWindow: NSWindow {
  // performDrag needs the mouse-down event, but the Dart call arrives after
  // the pan gesture starts, when NSApp.currentEvent is no longer the mouse-down.
  private var lastMouseDown: NSEvent?
  private var mouseDownMonitor: Any?

  override func awakeFromNib() {
    let flutterViewController = FlutterViewController()
    let windowFrame = self.frame
    self.contentViewController = flutterViewController
    self.setFrame(windowFrame, display: true)
    self.contentMinSize = NSSize(width: 420, height: 600)

    self.titlebarAppearsTransparent = true
    self.titleVisibility = .hidden
    self.styleMask.insert(.fullSizeContentView)

    mouseDownMonitor = NSEvent.addLocalMonitorForEvents(matching: .leftMouseDown) { [weak self] event in
      if event.window === self { self?.lastMouseDown = event }
      return event
    }

    let windowChannel = FlutterMethodChannel(
      name: "argus/window",
      binaryMessenger: flutterViewController.engine.binaryMessenger)
    windowChannel.setMethodCallHandler { [weak self] call, result in
      switch call.method {
      case "startDrag":
        if let event = self?.lastMouseDown { self?.performDrag(with: event) }
        result(nil)
      case "zoom":
        self?.performZoom(nil)
        result(nil)
      default:
        result(FlutterMethodNotImplemented)
      }
    }

    RegisterGeneratedPlugins(registry: flutterViewController)

    super.awakeFromNib()
  }

  deinit {
    if let monitor = mouseDownMonitor { NSEvent.removeMonitor(monitor) }
  }
}
