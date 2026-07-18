import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mnemonas_client/core/network/api_error.dart';

void main() {
  test('parses flat API errors, retry delay, and response warnings', () {
    final response = Response<dynamic>(
      requestOptions: RequestOptions(path: '/api/v1/files/report.pdf'),
      statusCode: 429,
      data: {
        'code': 'WRITE_BUSY',
        'message': 'write concurrency limit reached',
        'details': {'path': '/report.pdf'},
        'request_id': 'request-1',
      },
      headers: Headers.fromMap({
        'retry-after': ['3'],
        'warning': [
          '199 MnemoNAS "first warning, with comma", '
              '199 MnemoNAS "second warning"',
        ],
      }),
    );

    final error = ApiException.fromResponse(response);

    expect(error.code, 'WRITE_BUSY');
    expect(error.message, 'write concurrency limit reached');
    expect(error.requestId, 'request-1');
    expect(error.retryAfter, const Duration(seconds: 3));
    expect(error.warnings, hasLength(2));
    expect(error.hasStructuredServerError, isTrue);
  });

  test('parses nested authentication errors', () {
    final response = Response<dynamic>(
      requestOptions: RequestOptions(path: '/api/v1/auth/login'),
      statusCode: 401,
      data: {
        'success': false,
        'error': {
          'code': 'INVALID_CREDENTIALS',
          'message': 'invalid username or password',
        },
      },
    );

    final error = ApiException.fromResponse(response);

    expect(error.code, 'INVALID_CREDENTIALS');
    expect(error.isUnauthorized, isTrue);
    expect(error.hasStructuredServerError, isTrue);
  });

  test('parses an HTTP-date Retry-After value', () {
    final headers = Headers.fromMap({
      'retry-after': ['Sun, 19 Jul 2026 12:00:10 GMT'],
    });

    expect(
      parseRetryAfter(headers, now: DateTime.utc(2026, 7, 19, 12)),
      const Duration(seconds: 10),
    );
  });
}
