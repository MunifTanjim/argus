import Flutter
import UIKit

class SceneDelegate: FlutterSceneDelegate {

}

// Without deferral, iOS holds touches at the left edge while it checks for a
// system gesture, which delays the hold that peeks the projects drawer.
class RunnerViewController: FlutterViewController {
  override var preferredScreenEdgesDeferringSystemGestures: UIRectEdge { .left }
}
