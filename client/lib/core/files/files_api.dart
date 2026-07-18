import 'dart:async';
import 'dart:io';
import 'dart:math';

import 'package:dio/dio.dart';

import '../network/api_client.dart';
import '../network/api_error.dart';
import 'file_models.dart';
import 'file_path.dart';

typedef TransferProgress = void Function(int transferred, int total);

final class DownloadResult {
  const DownloadResult({
    required this.file,
    required this.bytesWritten,
    required this.warnings,
  });

  final File file;
  final int bytesWritten;
  final List<ApiWarning> warnings;
}

final class FilesApi {
  const FilesApi(this._client);

  final ApiClient _client;

  Future<ApiResponse<DirectoryListing>> list(
    String logicalPath, {
    CancelToken? cancelToken,
  }) {
    final encoded = encodeLogicalPath(logicalPath);
    return _client.requestEnvelope<DirectoryListing>(
      '/api/v1/files/$encoded',
      cancelToken: cancelToken,
      decode: (data) => DirectoryListing.fromJson(_requireMap(data)),
    );
  }

  Future<ApiResponse<FileMutationResult>> createDirectory(String logicalPath) {
    final encoded = encodeLogicalPath(logicalPath, allowRoot: false);
    return _client.requestEnvelope<FileMutationResult>(
      '/api/v1/directories/$encoded',
      method: 'POST',
      decode: (data) => FileMutationResult.fromJson(_requireMap(data)),
    );
  }

  Future<ApiResponse<PathMutationResult>> move({
    required String sourcePath,
    required String destinationPath,
  }) {
    final source = normalizeLogicalPath(sourcePath, allowRoot: false);
    final destination = normalizeLogicalPath(destinationPath, allowRoot: false);
    _requireDifferentPaths(source, destination);
    return _client.requestEnvelope<PathMutationResult>(
      '/api/v1/files-move',
      method: 'POST',
      data: {'from': source, 'to': destination},
      decode: (data) => _decodePathMutation(
        data,
        expectedSource: source,
        expectedDestination: destination,
      ),
    );
  }

  Future<ApiResponse<PathMutationResult>> rename({
    required String logicalPath,
    required String newName,
  }) {
    final source = normalizeLogicalPath(logicalPath, allowRoot: false);
    final name = validateLogicalName(newName);
    final separator = source.lastIndexOf('/');
    final parent = separator == 0 ? '' : source.substring(0, separator);
    return move(sourcePath: source, destinationPath: '$parent/$name');
  }

  Future<ApiResponse<PathMutationResult>> copy({
    required String sourcePath,
    required String destinationPath,
  }) {
    final source = normalizeLogicalPath(sourcePath, allowRoot: false);
    final destination = normalizeLogicalPath(destinationPath, allowRoot: false);
    _requireDifferentPaths(source, destination);
    return _client.requestEnvelope<PathMutationResult>(
      '/api/v1/files-copy',
      method: 'POST',
      data: {'from': source, 'to': destination},
      decode: (data) => _decodePathMutation(
        data,
        expectedSource: source,
        expectedDestination: destination,
      ),
    );
  }

  Future<ApiResponse<DeleteIntentSnapshot>> prepareDeleteIntent(
    Iterable<FileEntry> entries,
  ) {
    final observations = entries
        .map(DeleteTargetObservation.fromFileEntry)
        .toList(growable: false);
    _validateDeleteObservations(observations);
    return _client.requestEnvelope<DeleteIntentSnapshot>(
      '/api/v1/files-delete-intents',
      method: 'POST',
      data: {
        'targets': observations
            .map((observation) => observation.toJson())
            .toList(growable: false),
      },
      decode: (data) => DeleteIntentSnapshot.fromJson(
        _requireMap(data),
        serverBaseUrl: _client.endpoint.baseUrl,
        expectedTargets: observations,
      ),
    );
  }

  Future<ApiResponse<DeleteMutationResult>> delete(
    DeleteConfirmation confirmation,
  ) {
    if (confirmation.serverBaseUrl != _client.endpoint.baseUrl) {
      throw const FormatException(
        'Delete confirmation belongs to a different server',
      );
    }
    final encoded = encodeLogicalPath(
      confirmation.target.path,
      allowRoot: false,
    );
    return _client.requestEnvelope<DeleteMutationResult>(
      '/api/v1/files/$encoded',
      method: 'DELETE',
      queryParameters: {
        'expected_delete_mode': confirmation.policy.mode.wireValue,
        'expected_delete_policy_token': confirmation.policy.token,
        'expected_delete_target_token': confirmation.target.targetToken,
      },
      decode: (data) {
        final result = DeleteMutationResult.fromJson(_requireMap(data));
        if (result.path != confirmation.target.path) {
          throw const FormatException(
            'Deleted path does not match confirmation',
          );
        }
        return result;
      },
    );
  }

  Future<ApiResponse<FileMutationResult>> uploadFile({
    required String logicalPath,
    required String sourcePath,
    TransferProgress? onProgress,
    CancelToken? cancelToken,
  }) async {
    final encoded = encodeLogicalPath(logicalPath, allowRoot: false);
    final source = File(sourcePath);
    final stat = await source.stat();
    if (stat.type != FileSystemEntityType.file) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'UPLOAD_SOURCE_INVALID',
        message: 'The upload source is not a regular file',
      );
    }

    // Streaming request bodies cannot be replayed safely after a 401.
    await _client.ensureSessionValidity();
    return _client.requestEnvelope<FileMutationResult>(
      '/api/v1/files/$encoded',
      method: 'POST',
      data: source.openRead(),
      headers: {
        Headers.contentLengthHeader: stat.size,
        Headers.contentTypeHeader: 'application/octet-stream',
      },
      retryOnUnauthorized: false,
      onSendProgress: onProgress,
      cancelToken: cancelToken,
      decode: (data) => FileMutationResult.fromJson(_requireMap(data)),
    );
  }

  Future<DownloadResult> downloadFile({
    required String logicalPath,
    required String destinationPath,
    bool overwrite = false,
    TransferProgress? onProgress,
    CancelToken? cancelToken,
  }) async {
    final encoded = encodeLogicalPath(logicalPath, allowRoot: false);
    final destination = File(destinationPath);
    if (!overwrite && await destination.exists()) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'DOWNLOAD_DESTINATION_EXISTS',
        message: 'The download destination already exists',
      );
    }

    final parent = destination.parent;
    if (!await parent.exists()) {
      await parent.create(recursive: true);
    }
    final random = Random.secure().nextInt(1 << 32);
    final temporary = File(
      '${destination.path}.mnemonas-${DateTime.now().microsecondsSinceEpoch}'
      '-$random.part',
    );

    IOSink? sink;
    try {
      final response = await _client.request(
        '/api/v1/download/$encoded',
        responseType: ResponseType.stream,
        cancelToken: cancelToken,
      );
      final body = response.data;
      if (body is! ResponseBody) {
        throw const ApiException(
          kind: ApiFailureKind.invalidResponse,
          code: 'INVALID_DOWNLOAD_RESPONSE',
          message: 'The server returned an invalid download stream',
        );
      }

      final expectedLength = int.tryParse(
        response.headers.value(Headers.contentLengthHeader) ?? '',
      );
      var received = 0;
      sink = temporary.openWrite(mode: FileMode.writeOnly);
      await for (final chunk in body.stream) {
        sink.add(chunk);
        received += chunk.length;
        onProgress?.call(received, expectedLength ?? -1);
      }
      await sink.flush();
      await sink.close();
      sink = null;

      if (expectedLength != null && received != expectedLength) {
        throw const ApiException(
          kind: ApiFailureKind.invalidResponse,
          code: 'DOWNLOAD_TRUNCATED',
          message: 'The download ended before all bytes were received',
        );
      }
      if (overwrite && await destination.exists()) {
        await destination.delete();
      }
      final completed = await temporary.rename(destination.path);
      return DownloadResult(
        file: completed,
        bytesWritten: received,
        warnings: parseWarnings(response.headers),
      );
    } finally {
      await sink?.close();
      if (await temporary.exists()) {
        await temporary.delete();
      }
    }
  }
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

void _requireDifferentPaths(String source, String destination) {
  if (source == destination) {
    throw const FormatException('Source and destination must differ');
  }
}

PathMutationResult _decodePathMutation(
  Object? data, {
  required String expectedSource,
  required String expectedDestination,
}) {
  final result = PathMutationResult.fromJson(_requireMap(data));
  if (result.sourcePath != expectedSource ||
      result.destinationPath != expectedDestination) {
    throw const FormatException('Mutation response paths do not match request');
  }
  return result;
}

void _validateDeleteObservations(List<DeleteTargetObservation> observations) {
  if (observations.isEmpty || observations.length > 1000) {
    throw const FormatException(
      'Delete intent must contain between 1 and 1000 targets',
    );
  }

  final normalizedPaths = <String>[];
  for (final observation in observations) {
    final normalized = normalizeLogicalPath(observation.path, allowRoot: false);
    if (normalized != observation.path) {
      throw const FormatException('Delete target path is not normalized');
    }
    for (final existing in normalizedPaths) {
      if (existing == normalized ||
          existing.startsWith('$normalized/') ||
          normalized.startsWith('$existing/')) {
        throw const FormatException(
          'Delete targets must be unique and non-nested',
        );
      }
    }
    normalizedPaths.add(normalized);
  }
}
