import 'dart:async';
import 'dart:io';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mnemonas_client/core/auth/auth_models.dart';
import 'package:mnemonas_client/core/auth/session_store.dart';
import 'package:mnemonas_client/core/files/files_api.dart';
import 'package:mnemonas_client/core/network/api_client.dart';
import 'package:mnemonas_client/core/network/api_error.dart';
import 'package:mnemonas_client/core/server/server_endpoint.dart';

void main() {
  test('cancelling an upload aborts the streaming request', () async {
    final directory = await Directory.systemTemp.createTemp(
      'mnemonas-upload-cancel-',
    );
    addTearDown(() => directory.delete(recursive: true));
    final source = File('${directory.path}/payload.bin');
    await source.writeAsBytes(List<int>.filled(64 * 1024, 0x5a));

    final store = MemoryAuthSessionStore();
    final initialSnapshot = await store.snapshot();
    expect(
      await store.commitIfRevision(
        initialSnapshot.revision,
        AuthSession(
          serverBaseUrl: 'https://nas.example.com',
          tokens: AuthTokenPair(
            accessToken: 'access-token',
            refreshToken: 'refresh-token',
            expiresAt: DateTime.now().toUtc().add(const Duration(hours: 1)),
          ),
        ),
      ),
      isTrue,
    );
    final adapter = _BlockingUploadAdapter();
    final dio = Dio()..httpClientAdapter = adapter;
    final api = FilesApi(
      ApiClient(
        endpoint: ServerEndpoint.parse('https://nas.example.com'),
        sessionStore: store,
        dio: dio,
      ),
    );
    final cancelToken = CancelToken();

    final upload = api.uploadFile(
      logicalPath: '/payload.bin',
      sourcePath: source.path,
      cancelToken: cancelToken,
    );
    await adapter.started.future.timeout(const Duration(seconds: 2));
    cancelToken.cancel('test cancellation');

    await expectLater(
      upload,
      throwsA(
        isA<ApiException>().having(
          (error) => error.kind,
          'kind',
          ApiFailureKind.cancelled,
        ),
      ),
    );
  });
}

final class _BlockingUploadAdapter implements HttpClientAdapter {
  final Completer<void> started = Completer<void>();

  @override
  void close({bool force = false}) {}

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    started.complete();
    final cancellation = cancelFuture;
    if (cancellation == null) {
      throw StateError('The upload did not receive a cancellation future');
    }
    await cancellation;
    throw DioException(
      requestOptions: options,
      type: DioExceptionType.cancel,
      error: 'request cancelled',
    );
  }
}
