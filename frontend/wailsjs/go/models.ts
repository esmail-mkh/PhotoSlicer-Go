export namespace updater {
	
	export class UpdateInfo {
	    available: boolean;
	    current_version: string;
	    latest_version: string;
	    release_url: string;
	    release_name: string;
	    published_at: string;
	
	    static createFrom(source: any = {}) {
	        return new UpdateInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.available = source["available"];
	        this.current_version = source["current_version"];
	        this.latest_version = source["latest_version"];
	        this.release_url = source["release_url"];
	        this.release_name = source["release_name"];
	        this.published_at = source["published_at"];
	    }
	}

}

