import '../network/api_client.dart';
import '../network/api_error.dart';
import 'auth_models.dart';

final class EmptyResponse {
  const EmptyResponse();
}

final class PasswordChangeResult {
  const PasswordChangeResult({required this.persistenceWarning});

  final bool persistenceWarning;
}

final class AuthApi {
  const AuthApi(this._client);

  final ApiClient _client;

  Future<ApiResponse<LoginResult>> login({
    required String username,
    required String password,
  }) async {
    final snapshot = await _client.sessionSnapshot();
    final response = await _client
        .requestEnvelope<({AuthTokenPair tokens, AuthUser user})>(
          '/api/v1/auth/login',
          method: 'POST',
          authenticated: false,
          retryOnUnauthorized: false,
          data: {'username': username, 'password': password},
          decode: (data) {
            final json = _requireMap(data);
            final userJson = _requireMap(json['user']);
            return (
              tokens: AuthTokenPair.fromJson(json),
              user: AuthUser.fromJson(userJson),
            );
          },
        );

    final session = AuthSession(
      serverBaseUrl: _client.endpoint.baseUrl,
      tokens: response.data.tokens,
    );
    await _client.commitSession(snapshot.revision, session);
    return ApiResponse(
      data: LoginResult(user: response.data.user, session: session),
      statusCode: response.statusCode,
      message: response.message,
      requestId: response.requestId,
      warnings: response.warnings,
    );
  }

  Future<ApiResponse<AuthSession>> refresh() => _client.refreshSession();

  Future<ApiResponse<AuthUser>> me() {
    return _client.requestEnvelope<AuthUser>(
      '/api/v1/auth/me',
      decode: (data) {
        final json = _requireMap(data);
        return AuthUser.fromJson(_requireMap(json['user']));
      },
    );
  }

  Future<ApiResponse<EmptyResponse>> logout({
    void Function()? onLocalSessionCleared,
  }) async {
    final cleared = await _client.takeAndClearSession();
    onLocalSessionCleared?.call();
    final session = cleared.session;
    if (session == null || session.serverBaseUrl != _client.endpoint.baseUrl) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'AUTH_SESSION_MISSING',
        message: 'Sign in is required',
      );
    }
    return _client.requestEnvelope<EmptyResponse>(
      '/api/v1/auth/logout',
      method: 'POST',
      authenticated: false,
      retryOnUnauthorized: false,
      data: {'refresh_token': session.tokens.refreshToken},
      decode: (_) => const EmptyResponse(),
    );
  }

  Future<ApiResponse<PasswordChangeResult>> changePassword({
    required String currentPassword,
    required String newPassword,
    required String expectedUserId,
  }) async {
    final snapshot = await _client.sessionSnapshot();
    if (snapshot.session == null ||
        snapshot.session!.serverBaseUrl != _client.endpoint.baseUrl) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'AUTH_SESSION_MISSING',
        message: 'Sign in is required',
      );
    }
    return _client.requestEnvelope<PasswordChangeResult>(
      '/api/v1/auth/password',
      method: 'POST',
      retryOnUnauthorized: false,
      data: {
        'old_password': currentPassword,
        'new_password': newPassword,
        'expected_user_id': expectedUserId,
      },
      decode: (data) {
        final json = data == null
            ? const <String, dynamic>{}
            : _requireMap(data);
        return PasswordChangeResult(
          persistenceWarning: json['warning'] == true,
        );
      },
    );
  }

  Future<void> forgetSession() => _client.clearSession();
}

Map<String, dynamic> _requireMap(Object? value) {
  if (value is Map<String, dynamic>) {
    return value;
  }
  if (value is Map) {
    return Map<String, dynamic>.from(value);
  }
  throw const FormatException('Expected a JSON object');
}
