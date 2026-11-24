import Cocoa
import FlutterMacOS
import macos_window_utils

class MainFlutterWindow: NSWindow {
  override func awakeFromNib() {
    // The app is dark only; in the Light appearance the window material
    // would be near white under the cream text.
    self.appearance = NSAppearance(named: .darkAqua)
    let windowFrame = self.frame
    let macOSWindowUtilsViewController = MacOSWindowUtilsViewController()
    self.contentViewController = macOSWindowUtilsViewController
    // The container view would otherwise take the first key events.
    self.makeFirstResponder(macOSWindowUtilsViewController.flutterViewController.view)
    self.setFrame(windowFrame, display: true)
    self.contentMinSize = NSSize(width: 420, height: 600)

    MainFlutterWindowManipulator.start(mainFlutterWindow: self)
    // start() resets the chrome; set it here so the first frame already has
    // the unified toolbar row (52 px, traffic lights centered).
    self.titlebarAppearsTransparent = true
    self.titleVisibility = .hidden
    self.styleMask.insert(.fullSizeContentView)
    let toolbar = NSToolbar(identifier: "ArgusToolbar")
    toolbar.allowsUserCustomization = false
    self.toolbar = toolbar
    self.toolbarStyle = .unified
    self.titlebarSeparatorStyle = .none
    // Shows through where Flutter leaves the canvas clear (the side panel).
    (macOSWindowUtilsViewController.view as? NSVisualEffectView)?.material = .sidebar

    RegisterGeneratedPlugins(registry: macOSWindowUtilsViewController.flutterViewController)

    super.awakeFromNib()
  }
}
