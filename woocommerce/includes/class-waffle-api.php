<?php
defined( 'ABSPATH' ) || exit;

/** Thin Waffle API client. Returns the decoded body or a WP_Error, never throws. */
class Waffle_API {
	public static function settings() {
		return (array) get_option( 'woocommerce_waffle_settings', array() );
	}

	/** 'sandbox' unless the store explicitly turned it off. */
	public static function mode() {
		$s = self::settings();
		return isset( $s['sandbox'] ) && 'no' === $s['sandbox'] ? 'live' : 'sandbox';
	}

	/** The key for the current mode, or '' when missing or issued for the other mode. */
	public static function key() {
		$s   = self::settings();
		$key = isset( $s[ self::mode() . '_key' ] ) ? trim( $s[ self::mode() . '_key' ] ) : '';
		return 0 === strpos( $key, 'wf_' . self::mode() . '_' ) ? $key : '';
	}

	public static function secret() {
		$s = self::settings();
		return isset( $s[ self::mode() . '_secret' ] ) ? trim( $s[ self::mode() . '_secret' ] ) : '';
	}

	public static function pay_url( $charge_id ) {
		return apply_filters( 'waffle_wc_pay_base', 'https://getwaffle.id' ) . '/pay/' . rawurlencode( $charge_id );
	}

	public static function create_charge( array $body, $idempotency_key ) {
		return self::request( 'POST', '/v1/charges', $body, array( 'Idempotency-Key' => $idempotency_key ) );
	}

	public static function get_charge( $id ) {
		return self::request( 'GET', '/v1/charges/' . rawurlencode( $id ) );
	}

	private static function request( $method, $path, $body = null, array $headers = array() ) {
		$key = self::key();
		if ( '' === $key ) {
			return new WP_Error( 'waffle_no_key', 'API key Waffle belum diisi atau tidak cocok dengan mode.' );
		}
		$args = array(
			'method'      => $method,
			'timeout'     => 25,
			'redirection' => 0,
			'headers'     => array_merge(
				array(
					'Authorization' => 'Bearer ' . $key,
					'Content-Type'  => 'application/json',
				),
				$headers
			),
		);
		if ( null !== $body ) {
			$args['body'] = wp_json_encode( $body );
		}
		$res = wp_remote_request( apply_filters( 'waffle_wc_api_base', 'https://api.getwaffle.id' ) . $path, $args );
		if ( is_wp_error( $res ) ) {
			return $res;
		}
		$code = (int) wp_remote_retrieve_response_code( $res );
		$data = json_decode( wp_remote_retrieve_body( $res ), true );
		if ( $code < 200 || $code > 299 || ! is_array( $data ) ) {
			$message = is_array( $data ) && isset( $data['error'] ) && is_string( $data['error'] ) ? $data['error'] : 'HTTP ' . $code;
			return new WP_Error( 'waffle_http', $message, array( 'status' => $code ) );
		}
		return $data;
	}
}
