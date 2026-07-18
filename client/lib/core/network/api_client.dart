import 'dart:async';
import 'dart:convert';

import 'package:dio/dio.dart';

import '../auth/auth_models.dart';
import '../auth/session_store.dart';
import '../server/server_endpoint.dart';
import 'api_error.dart';

typedef JsonDecoder<T> = T Function(Object? json);
typedef Clock = DateTime Function();

final class ApiClient {
  ApiClient({
    required this.endpoint,
    required this.sessionStore,
    Dio? dio,
    Clock? clock,
  }) : _dio =
           dio ??
           Dio(
             BaseOptions(
               baseUrl: endpoint.baseUrl,
               connectTimeout: const Duration(seconds: 15),
               sendTimeout: const Duration(minutes: 5),
               receiveTimeout: const Duration(minutes: 5),
               followRedirects: false,
               headers: const {'Accept': 'application/json'},
             ),
           ),
       _clock = clock ?? DateTime.now {
    _dio.options.baseUrl = endpoint.baseUrl;
    _dio.interceptors.add(
      InterceptorsWrapper(onRequest: _authorize, onError: _recoverUnauthorized),
    );
  }

  static const _authenticatedKey = 'mnemonas.authenticated';
  static const _retryUnauthorizedKey = 'mnemonas.retryUnauthorized';
  static const _retriedKey = 'mnemonas.retried';
  static const _refreshWarningsKey = 'mnemonas.refreshWarnings';
  static const _refreshInterval = Duration(seconds: 30);

  final ServerEndpoint endpoint;
  final AuthSessionStore sessionStore;
  final Dio _dio;
  final Clock _clock;
  Future<_RefreshOutcome>? _refreshInFlight;
  bool _closed = false;

  Dio get dio => _dio;

  Future<Response<dynamic>> request(
    String path, {
    String method = 'GET',
    Object? data,
    Map<String, dynamic>? queryParameters,
    Map<String, dynamic>? headers,
    bool authenticated = true,
    bool retryOnUnauthorized = true,
    ResponseType responseType = ResponseType.json,
    ProgressCallback? onSendProgress,
    ProgressCallback? onReceiveProgress,
    CancelToken? cancelToken,
  }) async {
    try {
      return await _dio.request<dynamic>(
        path,
        data: data,
        queryParameters: queryParameters,
        options: Options(
          method: method,
          headers: headers,
          responseType: responseType,
          extra: {
            _authenticatedKey: authenticated,
            _retryUnauthorizedKey: retryOnUnauthorized,
          },
        ),
        onSendProgress: onSendProgress,
        onReceiveProgress: onReceiveProgress,
        cancelToken: cancelToken,
      );
    } on DioException catch (error) {
      throw ApiException.fromDio(error, now: _clock());
    }
  }

  Future<ApiResponse<T>> requestEnvelope<T>(
    String path, {
    required JsonDecoder<T> decode,
    String method = 'GET',
    Object? data,
    Map<String, dynamic>? queryParameters,
    Map<String, dynamic>? headers,
    bool authenticated = true,
    bool retryOnUnauthorized = true,
    ProgressCallback? onSendProgress,
    CancelToken? cancelToken,
  }) async {
    final response = await request(
      path,
      method: method,
      data: data,
      queryParameters: queryParameters,
      headers: headers,
      authenticated: authenticated,
      retryOnUnauthorized: retryOnUnauthorized,
      onSendProgress: onSendProgress,
      cancelToken: cancelToken,
    );
    return decodeEnvelope(response, decode);
  }

  ApiResponse<T> decodeEnvelope<T>(
    Response<dynamic> response,
    JsonDecoder<T> decode,
  ) {
    final body = _asJsonMap(response.data);
    if (body == null || body['success'] != true || !body.containsKey('data')) {
      throw ApiException(
        kind: ApiFailureKind.invalidResponse,
        statusCode: response.statusCode,
        code: 'INVALID_RESPONSE',
        message: 'The server returned an invalid response',
        warnings: _allWarnings(response),
      );
    }

    try {
      return ApiResponse(
        data: decode(body['data']),
        statusCode: response.statusCode ?? 0,
        message: body['message'] is String ? body['message'] as String : null,
        requestId: body['request_id'] is String
            ? body['request_id'] as String
            : null,
        warnings: _allWarnings(response),
      );
    } on ApiException {
      rethrow;
    } on Object catch (error) {
      throw ApiException(
        kind: ApiFailureKind.invalidResponse,
        statusCode: response.statusCode,
        code: 'INVALID_RESPONSE',
        message: 'The server returned an invalid response',
        warnings: _allWarnings(response),
        cause: error,
      );
    }
  }

  Future<AuthSessionSnapshot> sessionSnapshot() => _readSessionSnapshot();

  Future<void> commitSession(int expectedRevision, AuthSession session) async {
    if (_closed) {
      throw _authContextChanged();
    }
    final committed = await _commitSessionIfRevision(expectedRevision, session);
    if (!committed) {
      throw _authContextChanged();
    }
    if (_closed) {
      await _takeAndClearSessionIfRevision(expectedRevision + 1);
      throw _authContextChanged();
    }
  }

  Future<AuthSessionSnapshot> takeAndClearSession() => _takeAndClearSession();

  Future<void> clearSessionIfRevision(int expectedRevision) async {
    await _takeAndClearSessionIfRevision(expectedRevision);
  }

  Future<void> clearSession() async {
    await _takeAndClearSession();
  }

  void close({bool force = true}) {
    if (_closed) {
      return;
    }
    _closed = true;
    _dio.close(force: force);
  }

  Future<ApiResponse<AuthSession>> refreshSession() async {
    final outcome = await _refreshSingleFlight();
    return ApiResponse(
      data: outcome.session,
      statusCode: outcome.statusCode,
      message: outcome.message,
      warnings: outcome.warnings,
    );
  }

  Future<AuthSession> requireSession() async {
    final session = (await _readSessionSnapshot()).session;
    if (session == null || session.serverBaseUrl != endpoint.baseUrl) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'AUTH_SESSION_MISSING',
        message: 'Sign in is required',
      );
    }
    return session;
  }

  Future<AuthSession> ensureSessionValidity({
    Duration minimumValidity = const Duration(seconds: 15),
  }) async {
    final session = await requireSession();
    if (session.tokens.expiresAt.isAfter(
      _clock().toUtc().add(minimumValidity),
    )) {
      return session;
    }
    return (await refreshSession()).data;
  }

  void _authorize(
    RequestOptions options,
    RequestInterceptorHandler handler,
  ) async {
    if (options.extra[_authenticatedKey] != true) {
      handler.next(options);
      return;
    }

    try {
      var session = (await _readSessionSnapshot()).session;
      final activeRefresh = _refreshInFlight;
      if (session == null && activeRefresh != null) {
        session = (await activeRefresh).session;
      }
      if (session == null || session.serverBaseUrl != endpoint.baseUrl) {
        throw const ApiException(
          kind: ApiFailureKind.local,
          code: 'AUTH_SESSION_MISSING',
          message: 'Sign in is required',
        );
      }

      options.headers['Authorization'] =
          '${session.tokens.tokenType} ${session.tokens.accessToken}';
      handler.next(options);
    } on Object catch (error) {
      handler.reject(
        DioException(
          requestOptions: options,
          type: DioExceptionType.unknown,
          error: error,
        ),
      );
    }
  }

  void _recoverUnauthorized(
    DioException error,
    ErrorInterceptorHandler handler,
  ) async {
    final request = error.requestOptions;
    if (!_canRecoverUnauthorized(error)) {
      handler.next(error);
      return;
    }

    try {
      final attemptedToken = _bearerToken(request.headers['Authorization']);
      final activeRefresh = _refreshInFlight;
      late final _RefreshOutcome outcome;
      if (activeRefresh != null) {
        outcome = await activeRefresh;
      } else {
        final session = (await _readSessionSnapshot()).session;
        if (session == null || session.serverBaseUrl != endpoint.baseUrl) {
          throw const ApiException(
            kind: ApiFailureKind.local,
            code: 'AUTH_SESSION_MISSING',
            message: 'Sign in is required',
          );
        }
        outcome =
            attemptedToken != null &&
                attemptedToken != session.tokens.accessToken
            ? _RefreshOutcome(session: session)
            : await _refreshSingleFlight();
      }
      final retried = await _retry(request, outcome.session.tokens.accessToken);
      if (outcome.warnings.isNotEmpty) {
        retried.extra[_refreshWarningsKey] = outcome.warnings;
      }
      handler.resolve(retried);
    } on ApiException catch (apiError) {
      handler.reject(
        DioException(
          requestOptions: request,
          response: error.response,
          type: error.type,
          error: apiError,
        ),
      );
    } on DioException catch (retryError) {
      handler.next(retryError);
    } on Object catch (unexpected) {
      handler.reject(
        DioException(
          requestOptions: request,
          type: DioExceptionType.unknown,
          error: ApiException(
            kind: ApiFailureKind.invalidResponse,
            code: 'REFRESH_FAILED',
            message: 'Unable to renew the session',
            cause: unexpected,
          ),
        ),
      );
    }
  }

  bool _canRecoverUnauthorized(DioException error) {
    final request = error.requestOptions;
    if (error.response?.statusCode != 401 ||
        request.extra[_authenticatedKey] != true ||
        request.extra[_retryUnauthorizedKey] == false ||
        request.extra[_retriedKey] == true) {
      return false;
    }

    final failure = ApiException.fromResponse(error.response!, now: _clock());
    // Other 401 codes can describe revoked credentials or endpoint-specific
    // failures such as an incorrect current password. Replaying those requests
    // after a refresh would be unsafe and could hide the original result.
    return failure.code == 'TOKEN_EXPIRED';
  }

  Future<_RefreshOutcome> _refreshSingleFlight() {
    final active = _refreshInFlight;
    if (active != null) {
      return active;
    }

    late final Future<_RefreshOutcome> future;
    future = () async {
      try {
        return await _performRefresh();
      } finally {
        if (identical(_refreshInFlight, future)) {
          _refreshInFlight = null;
        }
      }
    }();
    _refreshInFlight = future;
    return future;
  }

  Future<_RefreshOutcome> _performRefresh() async {
    final snapshot = await _readSessionSnapshot();
    final current = snapshot.session;
    if (current == null || current.serverBaseUrl != endpoint.baseUrl) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'AUTH_SESSION_MISSING',
        message: 'Sign in is required',
      );
    }

    final now = _clock().toUtc();
    if (current.lastRefreshAt case final lastRefresh?) {
      final availableAt = lastRefresh.add(_refreshInterval);
      if (availableAt.isAfter(now)) {
        throw ApiException(
          kind: ApiFailureKind.local,
          code: 'REFRESH_COOLDOWN',
          message: 'Session renewal is temporarily limited',
          retryAfter: availableAt.difference(now),
        );
      }
    }

    final lease = await _takeAndClearSessionIfRevision(snapshot.revision);
    if (lease == null ||
        lease.session == null ||
        lease.session!.serverBaseUrl != endpoint.baseUrl) {
      throw _authContextChanged();
    }

    // The old rotating token is durably removed before it is sent. Any
    // unconfirmed refresh therefore fails closed instead of replaying a token
    // that the server may already have consumed.
    final response = await requestEnvelope<AuthTokenPair>(
      '/api/v1/auth/refresh',
      method: 'POST',
      authenticated: false,
      retryOnUnauthorized: false,
      data: {'refresh_token': lease.session!.tokens.refreshToken},
      decode: (data) {
        final map = _requireMap(data);
        return AuthTokenPair.fromJson(map);
      },
    );
    final rotated = lease.session!.rotated(response.data, now);
    await commitSession(lease.revision, rotated);
    return _RefreshOutcome(
      session: rotated,
      statusCode: response.statusCode,
      message: response.message,
      warnings: response.warnings,
    );
  }

  Future<Response<dynamic>> _retry(RequestOptions request, String accessToken) {
    final headers = Map<String, dynamic>.from(request.headers);
    headers['Authorization'] = 'Bearer $accessToken';
    final extra = Map<String, dynamic>.from(request.extra);
    extra[_retriedKey] = true;
    return _dio.fetch<dynamic>(
      request.copyWith(headers: headers, extra: extra),
    );
  }

  List<ApiWarning> _allWarnings(Response<dynamic> response) {
    final warnings = <ApiWarning>[...parseWarnings(response.headers)];
    final refreshWarnings = response.extra[_refreshWarningsKey];
    if (refreshWarnings is List<ApiWarning>) {
      warnings.insertAll(0, refreshWarnings);
    }
    return List.unmodifiable(warnings);
  }

  Future<AuthSessionSnapshot> _readSessionSnapshot() async {
    try {
      return await sessionStore.snapshot();
    } on AuthSessionStoreException catch (error) {
      throw _sessionStorageFailure(error);
    }
  }

  Future<bool> _commitSessionIfRevision(
    int expectedRevision,
    AuthSession session,
  ) async {
    try {
      return await sessionStore.commitIfRevision(expectedRevision, session);
    } on AuthSessionStoreException catch (error) {
      throw _sessionStorageFailure(error);
    }
  }

  Future<AuthSessionSnapshot?> _takeAndClearSessionIfRevision(
    int expectedRevision,
  ) async {
    try {
      return await sessionStore.takeAndClearIfRevision(expectedRevision);
    } on AuthSessionStoreException catch (error) {
      throw _sessionStorageFailure(error);
    }
  }

  Future<AuthSessionSnapshot> _takeAndClearSession() async {
    try {
      return await sessionStore.takeAndClear();
    } on AuthSessionStoreException catch (error) {
      throw _sessionStorageFailure(error);
    }
  }
}

final class _RefreshOutcome {
  const _RefreshOutcome({
    required this.session,
    this.statusCode = 200,
    this.message,
    this.warnings = const [],
  });

  final AuthSession session;
  final int statusCode;
  final String? message;
  final List<ApiWarning> warnings;
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

Map<String, dynamic>? _asJsonMap(Object? value) {
  if (value is String) {
    try {
      value = jsonDecode(value);
    } on FormatException {
      return null;
    }
  }
  if (value is Map<String, dynamic>) {
    return value;
  }
  if (value is Map) {
    return Map<String, dynamic>.from(value);
  }
  return null;
}

String? _bearerToken(Object? header) {
  if (header is! String) {
    return null;
  }
  final pieces = header.trim().split(RegExp(r'\s+'));
  if (pieces.length != 2 || pieces.first.toLowerCase() != 'bearer') {
    return null;
  }
  return pieces.last;
}

ApiException _authContextChanged() => const ApiException(
  kind: ApiFailureKind.local,
  code: 'AUTH_CONTEXT_CHANGED',
  message: 'The authentication context changed',
);

ApiException _sessionStorageFailure(Object cause) => ApiException(
  kind: ApiFailureKind.local,
  code: 'AUTH_SESSION_STORAGE_FAILED',
  message: 'Unable to update the local authentication state',
  cause: cause,
);
