import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:macos_window_utils/macos_window_utils.dart';
import 'package:macos_window_utils/widgets/macos_toolbar_passthrough.dart';
import 'package:argus/ui/theme.dart';
import 'package:argus/ui/titlebar.dart';

const _windowChannel = MethodChannel('macos_window_utils/window_manipulator');

// What the native side was last told: the passthrough rect per id.
final _passthroughRects = <String, Rect>{};

void _platform(TargetPlatform p) {
  debugDefaultTargetPlatformOverride = p;
  addTearDown(() => debugDefaultTargetPlatformOverride = null);
}

ValueNotifier<bool> _fullScreen([bool value = false]) {
  final n = ValueNotifier(value);
  addTearDown(n.dispose);
  return n;
}

Widget _app(Widget home) {
  final fullScreen = _fullScreen();
  final routes = TitlebarRouteObserver();
  addTearDown(routes.dispose);
  return MaterialApp(
    navigatorObservers: [routes],
    builder: (context, child) =>
        TitlebarScope(fullScreen: fullScreen, routes: routes, child: child!),
    home: home,
  );
}

Widget _scaffold(PreferredSizeWidget appBar) =>
    _app(Scaffold(appBar: appBar, body: const SizedBox.expand()));

Rect _passthroughRectOf(WidgetTester tester, Finder child) => tester.getRect(
  find.ancestor(of: child, matching: find.byType(MacosToolbarPassthrough)),
);

// Lets debounced passthrough updates (10 ms) and their post-frame resend run.
// The package's init future completed outside the fake-async zone, so its
// continuations need real event-loop turns.
Future<void> _flushPassthrough(WidgetTester tester) async {
  await tester.pumpAndSettle();
  for (var i = 0; i < 3; i++) {
    await tester.pump(const Duration(milliseconds: 20));
    await tester.runAsync(() => Future<void>.delayed(Duration.zero));
  }
  await tester.pump();
}

Set<Rect> _visiblePassthroughRects(WidgetTester tester) => find
    .byType(MacosToolbarPassthrough)
    .evaluate()
    .map((e) => tester.getRect(find.byElementPredicate((x) => x == e)))
    .toSet();

void main() {
  setUpAll(() async {
    TestWidgetsFlutterBinding.ensureInitialized();
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(_windowChannel, (call) async {
          final args = call.arguments as Map?;
          switch (call.method) {
            case 'updateToolbarPassthroughView':
              _passthroughRects[args!['id'] as String] = Rect.fromLTWH(
                args['x'] as double,
                args['y'] as double,
                args['width'] as double,
                args['height'] as double,
              );
            case 'removeToolbarPassthroughView':
              _passthroughRects.remove(args!['id']);
          }
          return true;
        });
    await WindowManipulator.initialize();
  });

  setUp(_passthroughRects.clear);

  testWidgets(
    'Android: ArgusAppBar builds an AppBar with the same title, actions and default height',
    (tester) async {
      _platform(TargetPlatform.android);
      await tester.pumpWidget(
        _scaffold(
          ArgusAppBar(
            title: const Text('Title'),
            actions: [
              IconButton(icon: const Icon(Icons.add), onPressed: () {}),
            ],
            titleSpacing: 7,
          ),
        ),
      );
      final bar = tester.widget<AppBar>(find.byType(AppBar));
      expect((bar.title as Text).data, 'Title');
      expect(bar.actions, hasLength(1));
      expect(bar.titleSpacing, 7);
      expect(tester.getSize(find.byType(AppBar)).height, kToolbarHeight);
      expect(tester.getTopLeft(find.byType(AppBar)).dx, 0);
      expect(find.byType(MacosToolbarPassthrough), findsNothing);
    },
  );

  testWidgets(
    'macOS: ArgusAppBar is 52 px high; its content starts at the TitlebarInsets left value (80, then 0)',
    (tester) async {
      _platform(TargetPlatform.macOS);
      final bar = find.byType(ArgusAppBar);
      final inner = find.byType(AppBar);

      await tester.pumpWidget(
        _scaffold(
          const ArgusAppBar(title: Text('Title'), backgroundColor: Colors.red),
        ),
      );
      expect(tester.getSize(bar).height, kMacToolbarHeight);
      expect(tester.getTopLeft(bar).dx, 0);
      expect(tester.getTopLeft(inner).dx, kTrafficLightsInset);
      // The background fills the row, under the traffic lights too.
      final fill = find.descendant(
        of: bar,
        matching: find.byWidgetPredicate(
          (w) => w is ColoredBox && w.color == Colors.red,
        ),
      );
      expect(
        tester.getRect(fill.first),
        Rect.fromLTWH(0, 0, tester.getSize(bar).width, kMacToolbarHeight),
      );

      await tester.pumpWidget(
        const MaterialApp(
          home: TitlebarInsets(
            left: 0,
            child: Scaffold(
              appBar: ArgusAppBar(title: Text('Title')),
              body: SizedBox.expand(),
            ),
          ),
        ),
      );
      expect(tester.getSize(bar).height, kMacToolbarHeight);
      expect(tester.getTopLeft(inner).dx, 0);
    },
  );

  testWidgets(
    'macOS: leading and action buttons are wrapped in the package passthrough widget; a tap on an action IconButton runs its onPressed',
    (tester) async {
      _platform(TargetPlatform.macOS);
      var pressed = 0;
      await tester.pumpWidget(
        _scaffold(
          ArgusAppBar(
            leading: IconButton(icon: const Icon(Icons.menu), onPressed: () {}),
            title: const Text('Title'),
            actions: [
              IconButton(
                icon: const Icon(Icons.add),
                onPressed: () => pressed++,
              ),
            ],
          ),
        ),
      );
      final passthrough = find.byType(MacosToolbarPassthrough);
      for (final icon in [Icons.menu, Icons.add]) {
        expect(
          find.ancestor(of: find.byIcon(icon), matching: passthrough),
          findsOneWidget,
        );
      }
      // A plain text title stays draggable, like a native title.
      expect(
        find.ancestor(of: find.text('Title'), matching: passthrough),
        findsNothing,
      );
      await tester.tap(find.byIcon(Icons.add));
      expect(pressed, 1);
    },
  );

  testWidgets(
    'macOS: an interactive title and the implied back button pass through',
    (tester) async {
      _platform(TargetPlatform.macOS);
      await tester.pumpWidget(
        _scaffold(const ArgusAppBar(title: Text('Root'))),
      );
      final nav = tester.state<NavigatorState>(find.byType(Navigator));
      nav.push(
        MaterialPageRoute<void>(
          builder: (_) => Scaffold(
            appBar: ArgusAppBar(
              title: TextButton(onPressed: () {}, child: const Text('Filter')),
            ),
          ),
        ),
      );
      await tester.pumpAndSettle();
      final passthrough = find.byType(MacosToolbarPassthrough);
      expect(
        find.ancestor(of: find.byType(BackButton), matching: passthrough),
        findsOneWidget,
      );
      expect(
        find.ancestor(of: find.text('Filter'), matching: passthrough),
        findsOneWidget,
      );
    },
  );

  group('TitlebarScope', () {
    Future<double Function()> pumpScope(
      WidgetTester tester, {
      ValueNotifier<bool>? fullScreen,
    }) async {
      late BuildContext inner;
      await tester.pumpWidget(
        TitlebarScope(
          fullScreen: fullScreen,
          child: Builder(
            builder: (context) {
              inner = context;
              return const SizedBox();
            },
          ),
        ),
      );
      return () => TitlebarInsets.of(inner);
    }

    testWidgets(
      'left is 80 on macOS, 0 when the injected full-screen listenable is true, 80 again when false',
      (tester) async {
        _platform(TargetPlatform.macOS);
        final fs = _fullScreen();
        final left = await pumpScope(tester, fullScreen: fs);
        expect(left(), kTrafficLightsInset);
        fs.value = true;
        await tester.pump();
        expect(left(), 0);
        fs.value = false;
        await tester.pump();
        expect(left(), kTrafficLightsInset);
      },
    );

    testWidgets(
      'on macOS without an injected listenable, left starts from the window full-screen state',
      (tester) async {
        _platform(TargetPlatform.macOS);
        late BuildContext inner;
        await tester.pumpWidget(
          TitlebarScope(
            child: Builder(
              builder: (context) {
                inner = context;
                return const SizedBox();
              },
            ),
          ),
        );
        expect(TitlebarInsets.of(inner), kTrafficLightsInset);
        // The mocked window answers true to isWindowFullscreened.
        await _flushPassthrough(tester);
        expect(TitlebarInsets.of(inner), 0);
      },
    );

    group('window delegate events', () {
      late FullScreenDelegate delegate;
      setUp(() => delegate = FullScreenDelegate());
      tearDown(() => delegate.dispose());

      test('the inset is restored when the window starts exiting full screen',
          () {
        delegate.windowWillEnterFullScreen();
        expect(delegate.value, isTrue);
        delegate.windowWillExitFullScreen();
        expect(delegate.value, isFalse);
      });

      test('a full-screen state seeded late does not override an event', () {
        delegate.windowWillEnterFullScreen();
        delegate.seed(false);
        expect(delegate.value, isTrue);
      });

      test('the exit still ends outside full screen', () {
        delegate.windowWillEnterFullScreen();
        delegate.windowDidExitFullScreen();
        expect(delegate.value, isFalse);
      });
    });

    testWidgets('left is 0 on Linux', (tester) async {
      _platform(TargetPlatform.linux);
      final left = await pumpScope(tester, fullScreen: _fullScreen());
      expect(left(), 0);
    });

    testWidgets('of is 0 without a scope', (tester) async {
      _platform(TargetPlatform.macOS);
      late BuildContext ctx;
      await tester.pumpWidget(
        Builder(
          builder: (context) {
            ctx = context;
            return const SizedBox();
          },
        ),
      );
      expect(TitlebarInsets.of(ctx), 0);
    });
  });

  group('TitlebarSpacer', () {
    testWidgets('52 px drag area on macOS', (tester) async {
      _platform(TargetPlatform.macOS);
      await tester.pumpWidget(const Column(children: [TitlebarSpacer()]));
      expect(
        tester.getSize(find.byType(TitlebarSpacer)),
        Size(tester.getSize(find.byType(Column)).width, kMacToolbarHeight),
      );
    });

    testWidgets('zero size on Android', (tester) async {
      _platform(TargetPlatform.android);
      await tester.pumpWidget(const Column(children: [TitlebarSpacer()]));
      expect(tester.getSize(find.byType(TitlebarSpacer)), Size.zero);
    });
  });

  group('passthrough on macOS', () {
    testWidgets(
      'after a push settles, the native rects are those of the pushed route only',
      (tester) async {
        _platform(TargetPlatform.macOS);
        await tester.pumpWidget(
          _scaffold(
            ArgusAppBar(
              title: const Text('Root'),
              actions: [
                IconButton(icon: const Icon(Icons.add), onPressed: () {}),
              ],
            ),
          ),
        );
        await _flushPassthrough(tester);
        expect(
          _passthroughRects.values.toSet(),
          _visiblePassthroughRects(tester),
        );
        expect(_passthroughRects, hasLength(1));

        tester
            .state<NavigatorState>(find.byType(Navigator))
            .push(
              MaterialPageRoute<void>(
                builder: (_) => Scaffold(
                  appBar: ArgusAppBar(
                    title: const Text('Pushed'),
                    actions: [
                      IconButton(
                        icon: const Icon(Icons.edit),
                        onPressed: () {},
                      ),
                    ],
                  ),
                ),
              ),
            );
        await _flushPassthrough(tester);
        final back = tester.getRect(
          find.ancestor(
            of: find.byType(BackButton),
            matching: find.byType(MacosToolbarPassthrough),
          ),
        );
        expect(_passthroughRects.values, contains(back));
        // The covered route's toolbar no longer blocks native drag.
        expect(
          _passthroughRects.values.toSet(),
          _visiblePassthroughRects(tester),
        );
        expect(_passthroughRects, hasLength(2));
      },
    );

    testWidgets(
      'after a pop settles, the native rects are the root route\'s again',
      (tester) async {
        _platform(TargetPlatform.macOS);
        await tester.pumpWidget(
          _scaffold(
            ArgusAppBar(
              title: const Text('Root'),
              actions: [
                IconButton(icon: const Icon(Icons.add), onPressed: () {}),
              ],
            ),
          ),
        );
        final nav = tester.state<NavigatorState>(find.byType(Navigator));
        nav.push(
          MaterialPageRoute<void>(
            builder: (_) =>
                const Scaffold(appBar: ArgusAppBar(title: Text('P'))),
          ),
        );
        await _flushPassthrough(tester);
        nav.pop();
        await _flushPassthrough(tester);
        expect(
          _passthroughRects.values.toSet(),
          _visiblePassthroughRects(tester),
        );
        expect(_passthroughRects, hasLength(1));
      },
    );

    testWidgets(
      'the toolbar of a hidden IndexedStack child passes nothing through',
      (tester) async {
        _platform(TargetPlatform.macOS);
        Widget tab(IconData icon) => Scaffold(
          appBar: ArgusAppBar(
            actions: [IconButton(icon: Icon(icon), onPressed: () {})],
          ),
        );
        await tester.pumpWidget(
          _app(IndexedStack(children: [tab(Icons.add), tab(Icons.edit)])),
        );
        await _flushPassthrough(tester);
        // Both toolbars put their button at the same place.
        expect(_passthroughRects, hasLength(1));
      },
    );

    testWidgets(
      'an interactive title keeps its state when its route is covered',
      (tester) async {
        _platform(TargetPlatform.macOS);
        final controller = TextEditingController();
        addTearDown(controller.dispose);
        await tester.pumpWidget(
          _scaffold(ArgusAppBar(title: TextField(controller: controller))),
        );
        await tester.enterText(find.byType(TextField), 'kept');
        final state = tester.state(find.byType(EditableText));
        final nav = tester.state<NavigatorState>(find.byType(Navigator));
        nav.push(MaterialPageRoute<void>(builder: (_) => const Scaffold()));
        await _flushPassthrough(tester);
        nav.pop();
        await _flushPassthrough(tester);
        expect(tester.state(find.byType(EditableText)), same(state));
      },
    );
  });

  testWidgets('Linux: ArgusAppBar is 40 px under the desktop theme', (
    tester,
  ) async {
    _platform(TargetPlatform.linux);
    await tester.pumpWidget(
      MaterialApp(
        theme: buildArgusTheme(platform: TargetPlatform.linux),
        home: const Scaffold(appBar: ArgusAppBar(title: Text('T'))),
      ),
    );
    expect(tester.getSize(find.byType(ArgusAppBar)).height, 40);
  });

  group('passthrough with a nested navigator on macOS', () {
    // The root route holds a nested navigator, like HomeShell.
    Widget nested() => Scaffold(
      body: Navigator(
        onGenerateRoute: (_) => MaterialPageRoute<void>(
          builder: (_) => Scaffold(
            appBar: ArgusAppBar(
              title: const Text('Inner'),
              actions: [
                IconButton(icon: const Icon(Icons.add), onPressed: () {}),
                PopupMenuButton<int>(
                  icon: const Icon(Icons.more_vert),
                  itemBuilder: (_) => const [
                    PopupMenuItem(value: 1, child: Text('One')),
                  ],
                ),
              ],
            ),
          ),
        ),
      ),
    );

    NavigatorState rootNavigator(WidgetTester tester) =>
        tester.state<NavigatorState>(find.byType(Navigator).first);

    testWidgets(
      'a page in the nested navigator does not rebuild when a root route is pushed or popped',
      (tester) async {
        _platform(TargetPlatform.macOS);
        var builds = 0;
        await tester.pumpWidget(
          _app(
            Scaffold(
              body: Navigator(
                onGenerateRoute: (_) => MaterialPageRoute<void>(
                  builder: (_) {
                    builds++;
                    return const Scaffold(
                      appBar: ArgusAppBar(title: Text('Inner')),
                    );
                  },
                ),
              ),
            ),
          ),
        );
        await _flushPassthrough(tester);
        final before = builds;
        showDialog<void>(
          context: rootNavigator(tester).context,
          builder: (_) => const Text('dialog'),
        );
        await _flushPassthrough(tester);
        rootNavigator(tester).pop();
        await _flushPassthrough(tester);
        expect(builds, before);
      },
    );

    testWidgets(
      'after a root route over it pops, the inner bar\'s rects are current',
      (tester) async {
        _platform(TargetPlatform.macOS);
        await tester.pumpWidget(_app(nested()));
        await _flushPassthrough(tester);
        rootNavigator(tester).push(
          MaterialPageRoute<void>(
            builder: (_) =>
                const Scaffold(appBar: ArgusAppBar(title: Text('Settings'))),
          ),
        );
        await _flushPassthrough(tester);
        rootNavigator(tester).pop();
        await _flushPassthrough(tester);
        expect(
          _passthroughRects.values,
          contains(_passthroughRectOf(tester, find.byIcon(Icons.add))),
        );
        expect(
          _passthroughRects.values.toSet(),
          _visiblePassthroughRects(tester),
        );
      },
    );

    testWidgets(
      'with a menu open, the whole row passes through; after it closes, the buttons do again',
      (tester) async {
        _platform(TargetPlatform.macOS);
        await tester.pumpWidget(_app(nested()));
        await _flushPassthrough(tester);
        final buttons = _passthroughRects.values.toSet();
        expect(buttons, hasLength(2));

        await tester.tap(find.byIcon(Icons.more_vert));
        await _flushPassthrough(tester);
        expect(find.text('One'), findsOneWidget);
        expect(_passthroughRects.values.toSet(), {
          const Rect.fromLTWH(
            kTrafficLightsInset,
            0,
            800 - kTrafficLightsInset,
            kMacToolbarHeight,
          ),
        });

        await tester.tapAt(const Offset(400, 400));
        await _flushPassthrough(tester);
        expect(find.text('One'), findsNothing);
        expect(_passthroughRects.values.toSet(), buttons);
      },
    );

    testWidgets(
      'with a dialog on the root navigator, the inner bar\'s whole row passes through',
      (tester) async {
        _platform(TargetPlatform.macOS);
        await tester.pumpWidget(_app(nested()));
        await _flushPassthrough(tester);
        final buttons = _passthroughRects.values.toSet();
        showDialog<void>(
          context: rootNavigator(tester).context,
          builder: (_) => const Text('dialog'),
        );
        await _flushPassthrough(tester);
        expect(_passthroughRects.values.toSet(), {
          const Rect.fromLTWH(
            kTrafficLightsInset,
            0,
            800 - kTrafficLightsInset,
            kMacToolbarHeight,
          ),
        });
        rootNavigator(tester).pop();
        await _flushPassthrough(tester);
        expect(_passthroughRects.values.toSet(), buttons);
      },
    );
  });
}
