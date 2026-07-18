import 'package:dio/dio.dart';

enum ApiFailureKind {
  response,
  connection,
  timeout,
  cancelled,
  invalidResponse,
  local,
}

final class ApiWarning {
  const ApiWarning(this.value);

  final String value;

  @override
  String toString() => value;
}

final class ApiResponse<T> {
  const ApiResponse({
    required this.data,
    required this.statusCode,
    this.message,
    this.requestId,
    this.warnings = const [],
  });

  final T data;
  final int statusCode;
  final String? message;
  final String? requestId;
  final List<ApiWarning> warnings;

  bool get hasWarnings => warnings.isNotEmpty;
}

final class ApiException implements Exception {
  const ApiException({
    required this.kind,
    required this.message,
    this.statusCode,
    this.code,
    this.details,
    this.requestId,
    this.retryAfter,
    this.warnings = const [],
    this.hasStructuredServerError = false,
    this.cause,
  });

  factory ApiException.fromDio(DioException exception, {DateTime? now}) {
    if (exception.error case final ApiException apiException) {
      return apiException;
    }

    final response = exception.response;
    if (response != null) {
      return ApiException.fromResponse(response, now: now);
    }

    final kind = switch (exception.type) {
      DioExceptionType.connectionTimeout ||
      DioExceptionType.sendTimeout ||
      DioExceptionType.receiveTimeout => ApiFailureKind.timeout,
      DioExceptionType.cancel => ApiFailureKind.cancelled,
      DioExceptionType.connectionError => ApiFailureKind.connection,
      _ => ApiFailureKind.invalidResponse,
    };
    return ApiException(
      kind: kind,
      message: switch (kind) {
        ApiFailureKind.timeout => 'The server did not respond in time',
        ApiFailureKind.cancelled => 'The request was cancelled',
        ApiFailureKind.connection => 'Unable to connect to the server',
        _ => 'The server returned an unreadable response',
      },
      cause: exception,
    );
  }

  factory ApiException.fromResponse(
    Response<dynamic> response, {
    DateTime? now,
  }) {
    final decoded = _decodeError(response.data);
    return ApiException(
      kind: ApiFailureKind.response,
      statusCode: response.statusCode,
      code: decoded.code ?? 'HTTP_${response.statusCode ?? 0}',
      message:
          decoded.message ??
          response.statusMessage ??
          'The server rejected the request',
      details: decoded.details,
      requestId: decoded.requestId,
      retryAfter: parseRetryAfter(response.headers, now: now),
      warnings: parseWarnings(response.headers),
      hasStructuredServerError: decoded.structured,
    );
  }

  final ApiFailureKind kind;
  final int? statusCode;
  final String? code;
  final String message;
  final Object? details;
  final String? requestId;
  final Duration? retryAfter;
  final List<ApiWarning> warnings;
  final bool hasStructuredServerError;
  final Object? cause;

  bool get isUnauthorized => statusCode == 401;

  bool get isUnconfirmedMutation =>
      kind != ApiFailureKind.response || !hasStructuredServerError;

  @override
  String toString() {
    final identifier = code ?? kind.name;
    return 'ApiException($identifier): $message';
  }
}

List<ApiWarning> parseWarnings(Headers headers) {
  final values = headers['warning'] ?? const [];
  return values
      .expand(_splitWarningHeader)
      .map((value) => ApiWarning(value.trim()))
      .where((warning) => warning.value.isNotEmpty)
      .toList(growable: false);
}

Duration? parseRetryAfter(Headers headers, {DateTime? now}) {
  final value = headers.value('retry-after')?.trim();
  if (value == null || value.isEmpty) {
    return null;
  }
  final seconds = int.tryParse(value);
  if (seconds != null) {
    return Duration(seconds: seconds < 0 ? 0 : seconds);
  }

  final deadline = _tryParseHttpDate(value);
  if (deadline == null) {
    return null;
  }
  final delay = deadline.difference((now ?? DateTime.now()).toUtc());
  return delay.isNegative ? Duration.zero : delay;
}

({
  String? code,
  String? message,
  Object? details,
  String? requestId,
  bool structured,
})
_decodeError(Object? body) {
  if (body is! Map) {
    return (
      code: null,
      message: body is String && body.trim().isNotEmpty ? body : null,
      details: null,
      requestId: null,
      structured: false,
    );
  }

  final map = Map<String, dynamic>.from(body);
  final nested = map['error'];
  final error = nested is Map ? Map<String, dynamic>.from(nested) : map;
  final code = error['code'] is String ? error['code'] as String : null;
  final message = error['message'] is String
      ? error['message'] as String
      : map['message'] is String
      ? map['message'] as String
      : null;
  return (
    code: code,
    message: message,
    details: error['details'] ?? map['details'],
    requestId: map['request_id'] is String ? map['request_id'] as String : null,
    structured: code != null && message != null,
  );
}

Iterable<String> _splitWarningHeader(String value) sync* {
  var quoted = false;
  var escaped = false;
  var start = 0;
  for (var index = 0; index < value.length; index++) {
    final character = value[index];
    if (escaped) {
      escaped = false;
      continue;
    }
    if (character == r'\') {
      escaped = true;
      continue;
    }
    if (character == '"') {
      quoted = !quoted;
      continue;
    }
    if (character == ',' && !quoted) {
      yield value.substring(start, index);
      start = index + 1;
    }
  }
  yield value.substring(start);
}

DateTime? _tryParseHttpDate(String value) {
  // Dio does not expose dart:io on web, so support the RFC 1123 form emitted
  // by HTTP servers without importing platform-specific libraries.
  final match = RegExp(
    r'^(?:Mon|Tue|Wed|Thu|Fri|Sat|Sun), (\d{2}) '
    r'(Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec) '
    r'(\d{4}) (\d{2}):(\d{2}):(\d{2}) GMT$',
  ).firstMatch(value);
  if (match == null) {
    return null;
  }
  const months = {
    'Jan': 1,
    'Feb': 2,
    'Mar': 3,
    'Apr': 4,
    'May': 5,
    'Jun': 6,
    'Jul': 7,
    'Aug': 8,
    'Sep': 9,
    'Oct': 10,
    'Nov': 11,
    'Dec': 12,
  };
  return DateTime.utc(
    int.parse(match.group(3)!),
    months[match.group(2)!]!,
    int.parse(match.group(1)!),
    int.parse(match.group(4)!),
    int.parse(match.group(5)!),
    int.parse(match.group(6)!),
  );
}
