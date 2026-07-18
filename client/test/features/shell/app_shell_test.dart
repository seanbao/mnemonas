import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mnemonas_client/design_system/design_system.dart';
import 'package:mnemonas_client/features/shell/app_shell.dart';

void main() {
  testWidgets('uses four bottom destinations on compact screens', (
    WidgetTester tester,
  ) async {
    await tester.binding.setSurfaceSize(const Size(390, 780));
    addTearDown(() => tester.binding.setSurfaceSize(null));
    AppDestination? selected;
    var searchCalls = 0;

    await tester.pumpWidget(
      _app(
        AppShell(
          destination: AppDestination.home,
          onDestinationSelected: (AppDestination value) => selected = value,
          onSearch: () => searchCalls++,
          child: const Center(child: Text('内容')),
        ),
      ),
    );

    expect(find.byType(NavigationBar), findsOneWidget);
    expect(find.byType(NavigationRail), findsNothing);
    for (final String label in <String>['首页', '文件', '相册', '我的']) {
      expect(find.text(label), findsWidgets);
    }

    await tester.tap(find.text('文件').last);
    await tester.pump();
    expect(selected, AppDestination.files);
    await tester.tap(find.byKey(const Key('app-shell-search')));
    expect(searchCalls, 1);
  });

  testWidgets('switches to a navigation rail on wide screens', (
    WidgetTester tester,
  ) async {
    await tester.binding.setSurfaceSize(const Size(1200, 800));
    addTearDown(() => tester.binding.setSurfaceSize(null));

    await tester.pumpWidget(
      _app(
        AppShell(
          destination: AppDestination.photos,
          onDestinationSelected: (_) {},
          onSearch: () {},
          child: const Center(child: Text('内容')),
        ),
      ),
    );

    expect(find.byType(NavigationRail), findsOneWidget);
    expect(find.byType(NavigationBar), findsNothing);
    expect(
      tester.widget<NavigationRail>(find.byType(NavigationRail)).extended,
      isTrue,
    );
  });
}

Widget _app(Widget home) {
  return MaterialApp(theme: MnemoTheme.light, home: home);
}
