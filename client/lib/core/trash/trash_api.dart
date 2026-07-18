import 'package:dio/dio.dart';

import '../files/file_path.dart';
import '../network/api_client.dart';
import '../network/api_error.dart';
import 'trash_models.dart';

final class TrashApi {
  const TrashApi(this._client);

  final ApiClient _client;

  Future<ApiResponse<TrashListing>> list({CancelToken? cancelToken}) {
    return _client.requestEnvelope<TrashListing>(
      '/api/v1/trash/',
      cancelToken: cancelToken,
      decode: (data) => TrashListing.fromJson(_requireMap(data)),
    );
  }

  Future<ApiResponse<TrashRestoreResult>> restore({
    required String id,
    String? destinationPath,
    CancelToken? cancelToken,
  }) {
    final canonicalId = validateTrashItemId(id);
    final normalizedDestination = destinationPath == null
        ? null
        : normalizeLogicalPath(destinationPath, allowRoot: false);
    return _client.requestEnvelope<TrashRestoreResult>(
      '/api/v1/trash/${Uri.encodeComponent(canonicalId)}/restore',
      method: 'POST',
      queryParameters: normalizedDestination == null
          ? null
          : {'path': normalizedDestination},
      cancelToken: cancelToken,
      decode: (data) => TrashRestoreResult.fromJson(
        _requireMap(data),
        expectedId: canonicalId,
      ),
    );
  }

  Future<ApiResponse<TrashDeleteResult>> deletePermanently(
    String id, {
    CancelToken? cancelToken,
  }) {
    final canonicalId = validateTrashItemId(id);
    return _client.requestEnvelope<TrashDeleteResult>(
      '/api/v1/trash/${Uri.encodeComponent(canonicalId)}',
      method: 'DELETE',
      cancelToken: cancelToken,
      decode: (data) => TrashDeleteResult.fromJson(
        _requireMap(data),
        expectedId: canonicalId,
      ),
    );
  }

  Future<ApiResponse<TrashEmptyResult>> emptySelection(
    TrashSelectionSnapshot selection, {
    CancelToken? cancelToken,
  }) {
    return _client.requestEnvelope<TrashEmptyResult>(
      '/api/v1/trash/empty',
      method: 'POST',
      data: selection.toJson(),
      cancelToken: cancelToken,
      decode: (data) =>
          TrashEmptyResult.fromJson(_requireMap(data), selection: selection),
    );
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
