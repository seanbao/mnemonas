import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mnemonas_client/core/server/server_endpoint.dart';
import 'package:mnemonas_client/design_system/design_system.dart';
import 'package:mnemonas_client/features/connection/connection_page.dart';

void main() {
  testWidgets('validates a LAN endpoint and exposes its transport warning', (
    WidgetTester tester,
  ) async {
    ServerEndpoint? submitted;
    final Completer<ServerConnectionInfo> result =
        Completer<ServerConnectionInfo>();

    await tester.pumpWidget(
      _app(
        ConnectionPage(
          onValidate: (ServerEndpoint endpoint) {
            submitted = endpoint;
            return result.future;
          },
        ),
      ),
    );

    await tester.enterText(
      find.byKey(const Key('connection-address-field')),
      'http://192.168.1.20:8080',
    );
    await tester.pump();
    expect(find.textContaining('当前使用局域网 HTTP'), findsOneWidget);

    await tester.tap(find.byKey(const Key('connection-submit')));
    await tester.pump();
    expect(submitted?.baseUrl, 'http://192.168.1.20:8080');
    expect(find.text('正在验证设备'), findsOneWidget);

    result.complete(ServerConnectionInfo(endpoint: submitted!));
    await tester.pumpAndSettle();
    expect(find.text('验证并继续'), findsOneWidget);
  });

  testWidgets('rejects public HTTP before invoking validation', (
    WidgetTester tester,
  ) async {
    var calls = 0;
    await tester.pumpWidget(
      _app(
        ConnectionPage(
          onValidate: (ServerEndpoint endpoint) async {
            calls++;
            return ServerConnectionInfo(endpoint: endpoint);
          },
        ),
      ),
    );

    await tester.enterText(
      find.byKey(const Key('connection-address-field')),
      'http://example.com',
    );
    await tester.tap(find.byKey(const Key('connection-submit')));
    await tester.pump();

    expect(find.textContaining('公网设备必须使用 HTTPS'), findsOneWidget);
    expect(calls, 0);
  });
}

Widget _app(Widget home) {
  return MaterialApp(theme: MnemoTheme.light, home: home);
}
