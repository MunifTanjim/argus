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

    // The rest of the window chrome is set from Dart (main.dart).
    MainFlutterWindowManipulator.start(mainFlutterWindow: self)

    RegisterGeneratedPlugins(registry: macOSWindowUtilsViewController.flutterViewController)

    super.awakeFromNib()
  }
}
