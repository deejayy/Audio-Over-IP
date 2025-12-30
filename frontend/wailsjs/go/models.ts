export namespace main {
	
	export class ClientConfig {
	    defaultResampleOpts: pcmresample.Options;
	
	    static createFrom(source: any = {}) {
	        return new ClientConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.defaultResampleOpts = this.convertValues(source["defaultResampleOpts"], pcmresample.Options);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class PlaybackDevice {
	    deviceID: string;
	    name: string;
	    volume: number;
	    enabled: boolean;
	    isDefault: boolean;
	
	    static createFrom(source: any = {}) {
	        return new PlaybackDevice(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.deviceID = source["deviceID"];
	        this.name = source["name"];
	        this.volume = source["volume"];
	        this.enabled = source["enabled"];
	        this.isDefault = source["isDefault"];
	    }
	}
	export class Server {
	    ID: string;
	    Addr: string;
	    Hostname: string;
	    Online: boolean;
	    Status: string;
	    ResampleOpts: pcmresample.Options;
	    PlaybackDevices: PlaybackDevice[];
	    AudioConfig: wcatools.AudioConfig;
	    RemoteDeviceID: string;
	    RemoteDevices: wcatools.AudioConfig[];
	
	    static createFrom(source: any = {}) {
	        return new Server(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.Addr = source["Addr"];
	        this.Hostname = source["Hostname"];
	        this.Online = source["Online"];
	        this.Status = source["Status"];
	        this.ResampleOpts = this.convertValues(source["ResampleOpts"], pcmresample.Options);
	        this.PlaybackDevices = this.convertValues(source["PlaybackDevices"], PlaybackDevice);
	        this.AudioConfig = this.convertValues(source["AudioConfig"], wcatools.AudioConfig);
	        this.RemoteDeviceID = source["RemoteDeviceID"];
	        this.RemoteDevices = this.convertValues(source["RemoteDevices"], wcatools.AudioConfig);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ServerConfig {
	    port: string;
	
	    static createFrom(source: any = {}) {
	        return new ServerConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.port = source["port"];
	    }
	}

}

export namespace pcmresample {
	
	export class Options {
	    Method: number;
	    Quality: number;
	    MaxQuality: number;
	    MaxThreads: number;
	    CustomMixMatrix: number[][];
	    IncludeLFEInDownmix: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Options(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Method = source["Method"];
	        this.Quality = source["Quality"];
	        this.MaxQuality = source["MaxQuality"];
	        this.MaxThreads = source["MaxThreads"];
	        this.CustomMixMatrix = source["CustomMixMatrix"];
	        this.IncludeLFEInDownmix = source["IncludeLFEInDownmix"];
	    }
	}

}

export namespace wcatools {
	
	export class AudioConfig {
	    ID: string;
	    Name: string;
	    SamplesPerSec: number;
	    BitsPerSample: number;
	    Channels: number;
	    BlockAlign: number;
	    AvgBytesPerSec: number;
	    FormatTag: number;
	    CbSize: number;
	
	    static createFrom(source: any = {}) {
	        return new AudioConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.Name = source["Name"];
	        this.SamplesPerSec = source["SamplesPerSec"];
	        this.BitsPerSample = source["BitsPerSample"];
	        this.Channels = source["Channels"];
	        this.BlockAlign = source["BlockAlign"];
	        this.AvgBytesPerSec = source["AvgBytesPerSec"];
	        this.FormatTag = source["FormatTag"];
	        this.CbSize = source["CbSize"];
	    }
	}

}

