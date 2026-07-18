import 'dart:async';
import 'dart:io';
import 'dart:math';
import 'dart:typed_data';

import 'package:crypto/crypto.dart';
import 'package:dio/dio.dart';

import '../network/api_client.dart';
import '../network/api_error.dart';
import 'file_models.dart';
import 'file_path.dart';

typedef TransferProgress = void Function(int transferred, int total);
typedef TransferRequestStarted = void Function();
typedef DownloadCheckpointCallback =
    FutureOr<void> Function(DownloadCheckpoint checkpoint);

const downloadIdentityHeader = 'X-MnemoNAS-Download-Identity';
const downloadIdentityConditionHeader = 'X-MnemoNAS-If-Download-Identity';
const uploadOffsetHeader = 'Upload-Offset';
const uploadChunkIdHeader = 'Upload-Chunk-ID';
const uploadChunkSha256Header = 'X-MnemoNAS-Chunk-SHA256';
const maxUploadSessionChunkBytes = 8 * 1024 * 1024;

final class DownloadCheckpoint {
  const DownloadCheckpoint({
    required this.validator,
    required this.durableOffset,
    required this.totalBytes,
  });

  final String validator;
  final int durableOffset;
  final int totalBytes;
}

final class DownloadResult {
  const DownloadResult({
    required this.file,
    required this.bytesWritten,
    required this.validator,
    required this.totalBytes,
    required this.warnings,
  });

  final File file;
  final int bytesWritten;
  final String validator;
  final int totalBytes;
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
    TransferRequestStarted? onRequestStarted,
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
    onRequestStarted?.call();
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

  Future<ApiResponse<UploadSessionSnapshot>> createUploadSession({
    required String logicalPath,
    required int totalBytes,
    required String clientRequestId,
    CancelToken? cancelToken,
  }) {
    final path = normalizeLogicalPath(logicalPath, allowRoot: false);
    if (totalBytes < 0) {
      throw ArgumentError.value(
        totalBytes,
        'totalBytes',
        'Upload size cannot be negative',
      );
    }
    _requireUploadIdentifier(clientRequestId, 'clientRequestId');

    return _client.requestEnvelope<UploadSessionSnapshot>(
      '/api/v1/upload-sessions',
      method: 'POST',
      data: <String, Object>{
        'path': path,
        'total_bytes': totalBytes,
        'client_request_id': clientRequestId,
      },
      cancelToken: cancelToken,
      decode: (data) {
        final session = UploadSessionSnapshot.fromJson(_requireMap(data));
        if (session.path != path || session.totalBytes != totalBytes) {
          throw const FormatException(
            'Upload session does not match the create request',
          );
        }
        return session;
      },
    );
  }

  Future<ApiResponse<UploadSessionSnapshot>>
  lookupUploadSessionByClientRequestId({
    required String clientRequestId,
    required String logicalPath,
    required int totalBytes,
    CancelToken? cancelToken,
  }) {
    final requestId = _requireUploadIdentifier(
      clientRequestId,
      'clientRequestId',
    );
    final path = normalizeLogicalPath(logicalPath, allowRoot: false);
    if (totalBytes < 0) {
      throw ArgumentError.value(
        totalBytes,
        'totalBytes',
        'Upload size cannot be negative',
      );
    }

    return _client.requestEnvelope<UploadSessionSnapshot>(
      '/api/v1/upload-sessions/by-client-request/'
      '${Uri.encodeComponent(requestId)}',
      cancelToken: cancelToken,
      decode: (data) {
        final session = UploadSessionSnapshot.fromJson(_requireMap(data));
        if (session.path != path || session.totalBytes != totalBytes) {
          throw const FormatException(
            'Upload session does not match the lookup request',
          );
        }
        return session;
      },
    );
  }

  Future<ApiResponse<UploadSessionSnapshot>> getUploadSessionStatus({
    required String sessionId,
    CancelToken? cancelToken,
  }) {
    final id = _requireUploadIdentifier(sessionId, 'sessionId');
    return _client.requestEnvelope<UploadSessionSnapshot>(
      '/api/v1/upload-sessions/${Uri.encodeComponent(id)}',
      cancelToken: cancelToken,
      decode: (data) => _decodeUploadSession(data, expectedId: id),
    );
  }

  Future<ApiResponse<UploadSessionSnapshot>> uploadSessionChunk({
    required String sessionId,
    required String sourcePath,
    required int offset,
    required int length,
    required String chunkId,
    TransferProgress? onProgress,
    CancelToken? cancelToken,
  }) async {
    final id = _requireUploadIdentifier(sessionId, 'sessionId');
    final normalizedChunkId = _requireUploadIdentifier(chunkId, 'chunkId');
    if (offset < 0) {
      throw ArgumentError.value(
        offset,
        'offset',
        'Upload offset cannot be negative',
      );
    }
    if (length <= 0 || length > maxUploadSessionChunkBytes) {
      throw ArgumentError.value(
        length,
        'length',
        'Upload chunk length must be between 1 byte and 8 MiB',
      );
    }

    final source = File(sourcePath);
    final before = await source.stat();
    if (before.type != FileSystemEntityType.file ||
        offset > before.size ||
        length > before.size - offset) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'UPLOAD_CHUNK_SOURCE_INVALID',
        message: 'The upload chunk source is unavailable or too short',
      );
    }

    RandomAccessFile? input;
    late final Uint8List bytes;
    try {
      input = await source.open();
      await input.setPosition(offset);
      bytes = await input.read(length);
    } finally {
      await input?.close();
    }
    final after = await source.stat();
    if (bytes.length != length ||
        after.type != FileSystemEntityType.file ||
        after.size != before.size) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'UPLOAD_CHUNK_SOURCE_CHANGED',
        message: 'The upload source changed while preparing a chunk',
      );
    }

    final digest = sha256.convert(bytes).toString();
    await _client.ensureSessionValidity();
    final requiredOffset = offset + length;
    return _client.requestEnvelope<UploadSessionSnapshot>(
      '/api/v1/upload-sessions/${Uri.encodeComponent(id)}',
      method: 'PATCH',
      data: bytes,
      headers: <String, Object>{
        uploadOffsetHeader: '$offset',
        uploadChunkIdHeader: normalizedChunkId,
        uploadChunkSha256Header: digest,
        Headers.contentLengthHeader: '$length',
        Headers.contentTypeHeader: 'application/octet-stream',
      },
      retryOnUnauthorized: false,
      cancelToken: cancelToken,
      onSendProgress: onProgress == null
          ? null
          : (sent, _) => onProgress(offset + sent, before.size),
      decode: (data) {
        final session = _decodeUploadSession(data, expectedId: id);
        if (session.totalBytes != before.size ||
            session.durableOffset < requiredOffset) {
          throw const FormatException(
            'Upload session did not acknowledge the complete chunk',
          );
        }
        return session;
      },
    );
  }

  Future<ApiResponse<UploadSessionSnapshot>> commitUploadSession({
    required String sessionId,
    CancelToken? cancelToken,
  }) {
    final id = _requireUploadIdentifier(sessionId, 'sessionId');
    return _client.requestEnvelope<UploadSessionSnapshot>(
      '/api/v1/upload-sessions/${Uri.encodeComponent(id)}/commit',
      method: 'POST',
      cancelToken: cancelToken,
      decode: (data) => _decodeUploadSession(data, expectedId: id),
    );
  }

  Future<ApiResponse<UploadSessionSnapshot>> cancelUploadSession({
    required String sessionId,
    CancelToken? cancelToken,
  }) {
    final id = _requireUploadIdentifier(sessionId, 'sessionId');
    return _client.requestEnvelope<UploadSessionSnapshot>(
      '/api/v1/upload-sessions/${Uri.encodeComponent(id)}',
      method: 'DELETE',
      cancelToken: cancelToken,
      decode: (data) => _decodeUploadSession(data, expectedId: id),
    );
  }

  Future<DownloadResult> downloadFile({
    required String logicalPath,
    required String destinationPath,
    bool overwrite = false,
    String? stagingPath,
    String? resumeValidator,
    int? expectedTotalBytes,
    bool preservePartialOnFailure = false,
    DownloadCheckpointCallback? onCheckpoint,
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
      stagingPath ??
          '${destination.path}.mnemonas-'
              '${DateTime.now().microsecondsSinceEpoch}-$random.part',
    );
    if (temporary.path == destination.path) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'DOWNLOAD_STAGING_INVALID',
        message: 'The download staging path must differ from the destination',
      );
    }
    if (!await temporary.parent.exists()) {
      await temporary.parent.create(recursive: true);
    }

    RandomAccessFile? output;
    var completed = false;
    try {
      final partialExists = await temporary.exists();
      var offset = partialExists ? await temporary.length() : 0;
      if (offset < 0 ||
          (expectedTotalBytes != null && offset > expectedTotalBytes)) {
        throw const ApiException(
          kind: ApiFailureKind.local,
          code: 'DOWNLOAD_PART_INVALID',
          message: 'The partial download has an invalid length',
        );
      }
      if (offset > 0 && resumeValidator == null) {
        await temporary.delete();
        offset = 0;
      }

      if (partialExists &&
          resumeValidator != null &&
          expectedTotalBytes != null &&
          offset == expectedTotalBytes) {
        output = await temporary.open(mode: FileMode.append);
        await output.flush();
        await output.close();
        output = null;
        if (await temporary.length() != expectedTotalBytes) {
          throw const ApiException(
            kind: ApiFailureKind.local,
            code: 'DOWNLOAD_PART_INVALID',
            message: 'The completed partial download changed before commit',
          );
        }
        await onCheckpoint?.call(
          DownloadCheckpoint(
            validator: resumeValidator,
            durableOffset: offset,
            totalBytes: expectedTotalBytes,
          ),
        );
        onProgress?.call(offset, expectedTotalBytes);
        final materialized = await _materializeDownload(
          source: temporary,
          destination: destination,
          overwrite: overwrite,
        );
        completed = true;
        return DownloadResult(
          file: materialized,
          bytesWritten: offset,
          validator: resumeValidator,
          totalBytes: expectedTotalBytes,
          warnings: const [],
        );
      }

      final requestHeaders = <String, dynamic>{};
      if (resumeValidator != null) {
        requestHeaders[downloadIdentityConditionHeader] = resumeValidator;
      }
      if (offset > 0) {
        requestHeaders['Range'] = 'bytes=$offset-';
      }
      final response = await _client.request(
        '/api/v1/download/$encoded',
        headers: requestHeaders,
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

      final validator = response.headers.value(downloadIdentityHeader)?.trim();
      if (validator == null || validator.isEmpty) {
        throw const ApiException(
          kind: ApiFailureKind.invalidResponse,
          code: 'DOWNLOAD_VALIDATOR_MISSING',
          message: 'The server did not provide a download identity',
        );
      }
      if (resumeValidator != null && validator != resumeValidator) {
        throw const ApiException(
          kind: ApiFailureKind.invalidResponse,
          code: 'DOWNLOAD_SOURCE_CHANGED',
          message: 'The source file changed before the download completed',
        );
      }

      final responseLength = int.tryParse(
        response.headers.value(Headers.contentLengthHeader) ?? '',
      );
      final statusCode = response.statusCode ?? 0;
      late final int totalBytes;
      if (offset > 0) {
        if (statusCode != HttpStatus.partialContent) {
          throw const ApiException(
            kind: ApiFailureKind.invalidResponse,
            code: 'DOWNLOAD_RESUME_REJECTED',
            message: 'The server did not honor the requested download range',
          );
        }
        final range = _parseContentRange(
          response.headers.value('content-range'),
        );
        if (range == null ||
            range.start != offset ||
            range.end < range.start ||
            range.total <= range.end ||
            responseLength == null ||
            responseLength != range.end - range.start + 1) {
          throw const ApiException(
            kind: ApiFailureKind.invalidResponse,
            code: 'DOWNLOAD_RANGE_INVALID',
            message: 'The server returned an invalid download range',
          );
        }
        totalBytes = range.total;
      } else {
        if (statusCode != HttpStatus.ok || responseLength == null) {
          throw const ApiException(
            kind: ApiFailureKind.invalidResponse,
            code: 'DOWNLOAD_LENGTH_INVALID',
            message: 'The server returned an invalid download length',
          );
        }
        totalBytes = responseLength;
      }
      if (expectedTotalBytes != null && totalBytes != expectedTotalBytes) {
        throw const ApiException(
          kind: ApiFailureKind.invalidResponse,
          code: 'DOWNLOAD_SOURCE_CHANGED',
          message: 'The source file changed before the download started',
        );
      }
      await onCheckpoint?.call(
        DownloadCheckpoint(
          validator: validator,
          durableOffset: offset,
          totalBytes: totalBytes,
        ),
      );

      output = await temporary.open(
        mode: offset == 0 ? FileMode.write : FileMode.append,
      );
      var received = offset;
      var bytesSinceCheckpoint = 0;
      await for (final chunk in body.stream) {
        await output.writeFrom(chunk);
        received += chunk.length;
        bytesSinceCheckpoint += chunk.length;
        onProgress?.call(received, totalBytes);
        if (bytesSinceCheckpoint >= _downloadCheckpointBytes) {
          await output.flush();
          await onCheckpoint?.call(
            DownloadCheckpoint(
              validator: validator,
              durableOffset: received,
              totalBytes: totalBytes,
            ),
          );
          bytesSinceCheckpoint = 0;
        }
      }
      await output.flush();
      await output.close();
      output = null;
      await onCheckpoint?.call(
        DownloadCheckpoint(
          validator: validator,
          durableOffset: received,
          totalBytes: totalBytes,
        ),
      );

      if (received != totalBytes || await temporary.length() != totalBytes) {
        throw const ApiException(
          kind: ApiFailureKind.invalidResponse,
          code: 'DOWNLOAD_TRUNCATED',
          message: 'The download ended before all bytes were received',
        );
      }
      final materialized = await _materializeDownload(
        source: temporary,
        destination: destination,
        overwrite: overwrite,
      );
      completed = true;
      return DownloadResult(
        file: materialized,
        bytesWritten: received,
        validator: validator,
        totalBytes: totalBytes,
        warnings: parseWarnings(response.headers),
      );
    } on DioException catch (error) {
      throw ApiException.fromDio(error);
    } finally {
      await output?.close();
      if ((!preservePartialOnFailure || completed) &&
          await temporary.exists()) {
        await temporary.delete();
      }
    }
  }
}

const _downloadCheckpointBytes = 4 * 1024 * 1024;
final RegExp _uploadIdentifierPattern = RegExp(
  r'^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$',
);

String _requireUploadIdentifier(String value, String argumentName) {
  if (!_uploadIdentifierPattern.hasMatch(value)) {
    throw ArgumentError.value(
      value,
      argumentName,
      'A valid upload protocol identifier is required',
    );
  }
  return value;
}

UploadSessionSnapshot _decodeUploadSession(
  Object? data, {
  required String expectedId,
}) {
  final session = UploadSessionSnapshot.fromJson(_requireMap(data));
  if (session.id != expectedId) {
    throw const FormatException(
      'Upload session response has an unexpected identity',
    );
  }
  return session;
}

final class _ContentRange {
  const _ContentRange({
    required this.start,
    required this.end,
    required this.total,
  });

  final int start;
  final int end;
  final int total;
}

_ContentRange? _parseContentRange(String? value) {
  final match = RegExp(
    r'^bytes ([0-9]+)-([0-9]+)/([0-9]+)$',
  ).firstMatch(value?.trim() ?? '');
  if (match == null) {
    return null;
  }
  final start = int.tryParse(match.group(1)!);
  final end = int.tryParse(match.group(2)!);
  final total = int.tryParse(match.group(3)!);
  if (start == null || end == null || total == null) {
    return null;
  }
  return _ContentRange(start: start, end: end, total: total);
}

Future<File> _materializeDownload({
  required File source,
  required File destination,
  required bool overwrite,
}) async {
  final nonce = DateTime.now().microsecondsSinceEpoch;
  final ready = File('${destination.path}.mnemonas-$nonce.ready');
  final backup = File('${destination.path}.mnemonas-$nonce.backup');
  RandomAccessFile? output;
  try {
    output = await ready.open(mode: FileMode.write);
    await for (final chunk in source.openRead()) {
      await output.writeFrom(chunk);
    }
    await output.flush();
    await output.close();
    output = null;

    final destinationExists = await destination.exists();
    if (destinationExists && !overwrite) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'DOWNLOAD_DESTINATION_EXISTS',
        message: 'The download destination already exists',
      );
    }
    if (destinationExists) {
      await destination.rename(backup.path);
    }
    try {
      await ready.rename(destination.path);
    } on Object {
      if (await backup.exists() && !await destination.exists()) {
        await backup.rename(destination.path);
      }
      rethrow;
    }
    if (await backup.exists()) {
      try {
        await backup.delete();
      } on FileSystemException {
        // The completed destination is authoritative; stale backup cleanup can
        // be retried independently.
      }
    }
    return destination;
  } finally {
    await output?.close();
    if (await ready.exists()) {
      await ready.delete();
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
