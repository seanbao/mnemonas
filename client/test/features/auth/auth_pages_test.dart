import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mnemonas_client/core/network/api_error.dart';
import 'package:mnemonas_client/design_system/design_system.dart';
import 'package:mnemonas_client/features/auth/force_password_change_page.dart';
import 'package:mnemonas_client/features/auth/login_page.dart';

void main() {
  testWidgets('login submits credentials and toggles password visibility', (
    WidgetTester tester,
  ) async {
    LoginCredentials? submitted;
    await tester.pumpWidget(
      _app(
        LoginPage(
          serverAddress: 'https://nas.example.com',
          deviceName: '家庭存储',
          onChangeServer: () {},
          onLogin: (LoginCredentials credentials) async {
            submitted = credentials;
          },
        ),
      ),
    );

    final Finder passwordField = find.byKey(const Key('login-password'));
    Finder editablePassword() =>
        find.descendant(of: passwordField, matching: find.byType(EditableText));
    expect(tester.widget<EditableText>(editablePassword()).obscureText, isTrue);
    await tester.tap(find.byKey(const Key('login-password-visibility')));
    await tester.pump();
    expect(
      tester.widget<EditableText>(editablePassword()).obscureText,
      isFalse,
    );

    await tester.enterText(find.byKey(const Key('login-username')), ' admin ');
    await tester.enterText(passwordField, 'private-password');
    await tester.tap(find.byKey(const Key('login-submit')));
    await tester.pumpAndSettle();

    expect(submitted?.username, 'admin');
    expect(submitted?.password, 'private-password');
  });

  testWidgets('login presents retry timing from rate limit errors', (
    WidgetTester tester,
  ) async {
    await tester.pumpWidget(
      _app(
        LoginPage(
          serverAddress: 'https://nas.example.com',
          onChangeServer: () {},
          onLogin: (_) async {
            throw const ApiException(
              kind: ApiFailureKind.response,
              statusCode: 429,
              code: 'LOGIN_RATE_LIMITED',
              message: 'rate limited',
              retryAfter: Duration(seconds: 12),
            );
          },
        ),
      ),
    );

    await tester.enterText(find.byKey(const Key('login-username')), 'admin');
    await tester.enterText(find.byKey(const Key('login-password')), 'password');
    await tester.tap(find.byKey(const Key('login-submit')));
    await tester.pumpAndSettle();

    expect(find.textContaining('请在 12 秒后再试'), findsOneWidget);
  });

  testWidgets('required password change validates and submits a new password', (
    WidgetTester tester,
  ) async {
    RequiredPasswordChange? submitted;
    await tester.pumpWidget(
      _app(
        ForcePasswordChangePage(
          username: 'admin',
          onLogout: () {},
          onSubmit: (RequiredPasswordChange change) async {
            submitted = change;
          },
        ),
      ),
    );

    await tester.enterText(
      find.byKey(const Key('password-change-current')),
      'old-password',
    );
    await tester.enterText(
      find.byKey(const Key('password-change-new')),
      'short',
    );
    await tester.enterText(
      find.byKey(const Key('password-change-confirm')),
      'short',
    );
    await tester.tap(find.byKey(const Key('password-change-submit')));
    await tester.pump();
    expect(find.textContaining('至少包含 8 个 UTF-8 字节'), findsOneWidget);
    expect(submitted, isNull);

    await tester.enterText(
      find.byKey(const Key('password-change-new')),
      'new-private-password',
    );
    await tester.enterText(
      find.byKey(const Key('password-change-confirm')),
      'new-private-password',
    );
    await tester.tap(find.byKey(const Key('password-change-submit')));
    await tester.pumpAndSettle();

    expect(submitted?.currentPassword, 'old-password');
    expect(submitted?.newPassword, 'new-private-password');
  });
}

Widget _app(Widget home) {
  return MaterialApp(theme: MnemoTheme.light, home: home);
}
