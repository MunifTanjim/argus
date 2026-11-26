import 'package:argus/ui/theme.dart';
import 'package:argus/ui/top_bar.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

Widget _app(Widget home) => MaterialApp(theme: buildArgusTheme(), home: home);

Widget _screen(TopBar bar) => Scaffold(appBar: bar);

void main() {
  testWidgets('with a leading icon, the title is as far from it as it is '
      'from the edge', (tester) async {
    await tester.pumpWidget(
      _app(
        _screen(
          TopBar(
            leading: IconButton(icon: const Icon(Icons.menu), onPressed: () {}),
            title: const Text('Title'),
          ),
        ),
      ),
    );
    final icon = tester.getRect(find.byIcon(Icons.menu));
    final title = tester.getRect(find.text('Title'));
    expect(title.left - icon.right, icon.left);
  });

  testWidgets('the implied back button counts as a leading icon', (
    tester,
  ) async {
    await tester.pumpWidget(_app(const SizedBox()));
    final nav = tester.state<NavigatorState>(find.byType(Navigator));
    nav.push(
      MaterialPageRoute<void>(
        builder: (_) => _screen(const TopBar(title: Text('Title'))),
      ),
    );
    await tester.pumpAndSettle();
    final icon = tester.getRect(
      find.descendant(of: find.byType(BackButton), matching: find.byType(Icon)),
    );
    final title = tester.getRect(find.text('Title'));
    expect(title.left - icon.right, icon.left);
  });

  testWidgets('without a leading icon, the title keeps its edge spacing', (
    tester,
  ) async {
    await tester.pumpWidget(
      _app(_screen(const TopBar(title: Text('Title')))),
    );
    expect(tester.getRect(find.text('Title')).left, 16);
  });

  testWidgets('no implied leading when automaticallyImplyLeading is false', (
    tester,
  ) async {
    await tester.pumpWidget(_app(const SizedBox()));
    final nav = tester.state<NavigatorState>(find.byType(Navigator));
    nav.push(
      MaterialPageRoute<void>(
        builder: (_) => _screen(
          const TopBar(automaticallyImplyLeading: false, title: Text('Title')),
        ),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.byType(BackButton), findsNothing);
    expect(tester.getRect(find.text('Title')).left, 16);
  });
}
