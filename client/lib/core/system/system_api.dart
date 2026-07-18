import 'package:dio/dio.dart';

import '../network/api_client.dart';
import '../network/api_error.dart';
import 'system_models.dart';

final class SystemApi {
  const SystemApi(this._client);

  final ApiClient _client;

  Future<ApiResponse<HealthStatus>> health() async {
    final response = await _client.request(
      '/health',
      authenticated: false,
      retryOnUnauthorized: false,
    );
    try {
      return ApiResponse(
        data: HealthStatus.fromJson(requireJsonMap(response.data)),
        statusCode: response.statusCode ?? 0,
        warnings: parseWarnings(response.headers),
      );
    } on FormatException catch (error) {
      throw ApiException(
        kind: ApiFailureKind.invalidResponse,
        statusCode: response.statusCode,
        code: 'INVALID_HEALTH_RESPONSE',
        message: 'The server returned an invalid health response',
        warnings: parseWarnings(response.headers),
        cause: error,
      );
    }
  }

  Future<ApiResponse<ServerVersion>> version() {
    return _client.requestEnvelope<ServerVersion>(
      '/api/v1/version',
      authenticated: false,
      retryOnUnauthorized: false,
      decode: (data) => ServerVersion.fromJson(requireJsonMap(data)),
    );
  }

  Future<ApiResponse<SetupStatus>> setup() async {
    final response = await _client.request(
      '/api/v1/setup/',
      authenticated: false,
      retryOnUnauthorized: false,
    );
    try {
      return ApiResponse(
        data: SetupStatus.fromJson(requireJsonMap(response.data)),
        statusCode: response.statusCode ?? 0,
        warnings: parseWarnings(response.headers),
      );
    } on FormatException catch (error) {
      throw ApiException(
        kind: ApiFailureKind.invalidResponse,
        statusCode: response.statusCode,
        code: 'INVALID_SETUP_RESPONSE',
        message: 'The server returned an invalid setup response',
        warnings: parseWarnings(response.headers),
        cause: error,
      );
    }
  }

  Future<ServerProbe> probe() async {
    final healthResponse = await health();
    final versionResponse = await version();
    final setupResponse = await setup();
    return ServerProbe(
      health: healthResponse.data,
      version: versionResponse.data,
      setup: setupResponse.data,
    );
  }

  Future<ApiResponse<StorageStats>> stats({CancelToken? cancelToken}) {
    return _client.requestEnvelope<StorageStats>(
      '/api/v1/stats',
      cancelToken: cancelToken,
      decode: (data) => StorageStats.fromJson(requireJsonMap(data)),
    );
  }
}
