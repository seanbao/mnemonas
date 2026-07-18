import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mnemonas_client/app/client_controller.dart';
import 'package:mnemonas_client/app/client_state.dart';
import 'package:mnemonas_client/core/auth/auth_models.dart';
import 'package:mnemonas_client/core/auth/session_store.dart';
import 'package:mnemonas_client/core/network/api_error.dart';
import 'package:mnemonas_client/core/server/server_endpoint.dart';

void main() {
  group('terminal authentication failures', () {
    const terminalCodes = <String>[
      'AUTH_SESSION_MISSING',
      'AUTH_SCOPE_CHANGED',
      'INVALID_AUTH_HEADER',
      'INVALID_TOKEN',
      'MISSING_AUTH_HEADER',
      'NOT_AUTHENTICATED',
      'TOKEN_EXPIRED',
      'TOKEN_REVOKED',
      'USER_DISABLED',
      'USER_NOT_FOUND',
    ];

    for (final code in terminalCodes) {
      test('$code clears the session and returns to login', () async {
        final store = _TrackingSessionStore(_session());
        final container = ProviderContainer(
          overrides: [
            authSessionStoreProvider.overrideWithValue(store),
            clientControllerProvider.overrideWith(_ReadyController.new),
          ],
        );
        addTearDown(container.dispose);

        final controller = container.read(clientControllerProvider.notifier);
        final handled = await controller.handleAuthenticationFailure(
          ApiException(
            kind: ApiFailureKind.response,
            statusCode: code == 'USER_DISABLED' ? 403 : 401,
            code: code,
            message: 'authentication rejected',
          ),
        );

        expect(handled, isTrue);
        expect(store.clearCount, 1);
        expect(await store.load(), isNull);

        final state = container.read(clientControllerProvider);
        expect(state.stage, ClientStage.needsLogin);
        expect(state.endpoint?.baseUrl, 'https://nas.example.com');
        expect(state.user, isNull);
        expect(state.currentPath, '/');
        expect(state.directory, isNull);
        expect(state.stats, isNull);
        expect(state.transfers, isEmpty);
        expect(state.isBusy, isFalse);
        expect(state.notice, isNull);
        expect(state.errorMessage, '登录状态已失效，请重新登录。');
      });
    }
  });

  test(
    'recoverable network and service errors keep the signed-in state',
    () async {
      final recoverableErrors = <ApiException>[
        const ApiException(
          kind: ApiFailureKind.connection,
          code: 'CONNECTION_FAILED',
          message: 'network unavailable',
        ),
        const ApiException(
          kind: ApiFailureKind.timeout,
          code: 'REQUEST_TIMEOUT',
          message: 'request timed out',
        ),
        const ApiException(
          kind: ApiFailureKind.response,
          statusCode: 503,
          code: 'TOKEN_STATE_UNAVAILABLE',
          message: 'token state unavailable',
        ),
        const ApiException(
          kind: ApiFailureKind.response,
          statusCode: 401,
          code: 'INVALID_PASSWORD',
          message: 'current password is incorrect',
        ),
      ];

      for (final error in recoverableErrors) {
        final store = _TrackingSessionStore(_session());
        final container = ProviderContainer(
          overrides: [
            authSessionStoreProvider.overrideWithValue(store),
            clientControllerProvider.overrideWith(_ReadyController.new),
          ],
        );

        final controller = container.read(clientControllerProvider.notifier);
        final handled = await controller.handleAuthenticationFailure(error);

        expect(handled, isFalse, reason: error.code);
        expect(store.clearCount, 0, reason: error.code);
        expect(await store.load(), isNotNull, reason: error.code);
        expect(
          container.read(clientControllerProvider).stage,
          ClientStage.ready,
          reason: error.code,
        );
        container.dispose();
      }
    },
  );
}

final class _ReadyController extends ClientController {
  @override
  ClientState build() {
    return ClientState(
      stage: ClientStage.ready,
      endpoint: ServerEndpoint.parse('https://nas.example.com'),
      user: const AuthUser(
        id: 'user-1',
        username: 'owner',
        role: 'admin',
        homeDirectory: '/',
        mustChangePassword: false,
      ),
      currentPath: '/photos',
      isBusy: true,
      notice: 'stale notice',
      transfers: const <ClientTransfer>[
        ClientTransfer(
          id: 'transfer-1',
          name: 'photo.jpg',
          direction: TransferDirection.download,
          status: TransferStatus.running,
          transferred: 16,
          total: 32,
        ),
      ],
    );
  }
}

final class _TrackingSessionStore implements AuthSessionStore {
  _TrackingSessionStore(this.session);

  AuthSession? session;
  int clearCount = 0;

  @override
  Future<void> clear() async {
    clearCount++;
    session = null;
  }

  @override
  Future<AuthSession?> load() async => session;

  @override
  Future<void> save(AuthSession value) async {
    session = value;
  }
}

AuthSession _session() {
  return AuthSession(
    serverBaseUrl: 'https://nas.example.com',
    tokens: AuthTokenPair(
      accessToken: 'access-token',
      refreshToken: 'refresh-token',
      expiresAt: DateTime.utc(2026, 7, 19, 13),
    ),
  );
}
