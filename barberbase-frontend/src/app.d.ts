import type { StaffJWTClaims } from './lib/api/client';

declare global {
	namespace App {
		interface Platform {
			env: Env;
			ctx: ExecutionContext;
			caches: CacheStorage;
			cf?: IncomingRequestCfProperties;
		}

		// interface Error {}
		interface Locals {
			staff?: StaffJWTClaims | null;
		}
		// interface PageData {}
		// interface PageState {}
	}
}

export {};
