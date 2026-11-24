import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:macos_window_utils/widgets/macos_toolbar_passthrough.dart';
import 'package:argus/ui/titlebar.dart';

void _platform(TargetPlatform p) {
  debugDefaultTargetPlatformOverride = p;
  addTearDown(() => debugDefaultTargetPlatformOverride = null);
}

ValueNotifier<bool> _fullScreen([bool value = false]) {
  final n = ValueNotifier(value);
  addTearDown(n.dispose);
  return n;
}

Widget _scaffold(PreferredSizeWidget appBar) {
  final fullScreen = _fullScreen();
  return MaterialApp(
    builder: (context, child) =>
        TitlebarScope(fullScreen: fullScreen, child: child!),
    home: Scaffold(appBar: appBar, body: const SizedBox.expand()),
  );
}

void main() {
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
}
